// SPDX-License-Identifier: Apache-2.0

// Package vanilla implements the backend.Client interface against
// the upstream Alertmanager v2 HTTP API. The Mimir wrapper composes
// a vanilla client with a prefix and tenant header (per ADR 0028)
// rather than maintaining a parallel implementation, so this package
// is the source of truth for "talk to /api/v2/...".
//
// Read endpoints (ListAlerts/AlertGroups/Silences/GetSilence/Receivers/
// Status) live in read.go; write endpoints (CreateSilence,
// UpdateSilence, ExpireSilence) live in write.go. Capability-gated
// methods stay as ErrUnsupported stubs in this package — Mimir's
// wrapper implements them when caps allow.
package vanilla

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// Compile-time assertions: *Client implements every facet of the
// public backend interface set. Reader and Writer are asserted
// individually so a future split or rename trips the build at the
// narrowest call site.
var (
	_ backend.Reader = (*Client)(nil)
	_ backend.Writer = (*Client)(nil)
	_ backend.Client = (*Client)(nil)
)

// Default request timeout when ClientConfig.Timeout is zero. Picked
// large enough for a slow backend with thousands of alerts but
// small enough that the polling loop does not stall on a hung
// request — the C1 backoff cap is poll_interval × 6, so a 30 s
// request timeout fits inside the smallest sensible poll_interval
// (1 m default).
const defaultRequestTimeout = 30 * time.Second

// defaultMaxResponseBodyBytes caps the response body the JSON decoder
// will read. Defends against memory exhaustion: a hostile backend that
// streams a multi-gigabyte payload would otherwise OOM the TUI
// process. 64 MiB is chosen high enough to handle every realistic
// /api/v2/ response (the largest, alerts at production scale, tops out
// in single-digit MB) and low enough that a slow leak surfaces as a
// decode error rather than memory pressure.
const defaultMaxResponseBodyBytes int64 = 64 << 20

// ClientConfig is the constructor input for New. Fields:
//
//   - BaseURL: scheme + host + port (no trailing slash, no /api/v2).
//     Required.
//   - Prefix: optional path prefix (Mimir uses "/alertmanager"). The
//     final URL is BaseURL + Prefix + "/api/v2/...".
//   - Transport: the http.RoundTripper carrying auth/tenant layers.
//     nil defaults to http.DefaultTransport.
//   - Timeout: per-request timeout. Zero defaults to
//     defaultRequestTimeout.
//   - Caps: capability flags from `a10r.yaml`. All flags are off on
//     vanilla today; the field exists so the Mimir wrapper can pass
//     real caps through.
type ClientConfig struct {
	BaseURL   string
	Prefix    string
	Transport http.RoundTripper
	Timeout   time.Duration
	Caps      backend.Caps
	// ExpectedHost is the host portion of BaseURL. When non-empty,
	// the constructed *http.Client installs a CheckRedirect that
	// refuses any redirect to a different origin — defense-in-depth
	// against credential replay: even if a future RoundTripper bug
	// re-injected credentials, the cross-origin redirect itself
	// never fires. Empty preserves the legacy behaviour (Go's
	// default CheckRedirect, which strips Authorization on
	// cross-origin but happily follows up to ten redirects).
	ExpectedHost string
}

// Client is the vanilla Alertmanager v2 backend. Constructed via
// New; safe for concurrent use across goroutines.
//
// `baseURL` is the un-prefixed root (cfg.BaseURL as-is) and is used
// by ProbeAlertmanagerMount to issue a probe that bypasses the
// configured prefix; `base` is the prefix-folded root every other
// request composes from.
type Client struct {
	baseURL      string
	base         string
	http         *http.Client
	caps         backend.Caps
	maxBodyBytes int64
}

// ErrInvalidBaseURL is returned by New when ClientConfig.BaseURL is
// empty or fails url.Parse — caught at startup so the operator sees
// the problem before the first poll.
var ErrInvalidBaseURL = errors.New("invalid backend URL")

// New constructs a Client from cfg. Validation is eager: an invalid
// BaseURL surfaces immediately rather than on the first request.
func New(cfg ClientConfig) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: BaseURL is required", ErrInvalidBaseURL)
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidBaseURL, cfg.BaseURL, err)
	}

	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}

	return &Client{
		baseURL: cfg.BaseURL,
		base:    cfg.BaseURL + cfg.Prefix,
		http: &http.Client{
			Transport:     transport,
			Timeout:       timeout,
			CheckRedirect: refuseCrossOriginRedirect(cfg.ExpectedHost),
		},
		caps:         cfg.Caps,
		maxBodyBytes: defaultMaxResponseBodyBytes,
	}, nil
}

// ErrCrossOriginRedirect is returned through http.Client.Do when
// the server responds with a redirect to a different origin. The
// classifier wraps it as ErrUnreachable's family so callers see a
// transport-level failure rather than a successful cross-origin
// fetch.
var ErrCrossOriginRedirect = errors.New("redirect to a different origin refused")

// refuseCrossOriginRedirect returns the http.Client.CheckRedirect
// callback that hard-stops a redirect chain whenever the next-hop
// host differs from the originally configured BaseURL host. Empty
// expectedHost short-circuits to a nil callback so http.Client
// keeps its default redirect behaviour (cap 10, strip Authorization
// on cross-origin) — used by tests that don't set ExpectedHost.
//
// Belt-and-braces with the auth/header RoundTrippers' host-pinning
// (see internal/backend/transport): the RTs already refuse to
// inject credentials on a mismatch, but a redirect itself can still
// hand the user's tenant identity to an attacker via the request
// path / cookie. This callback turns the entire redirect into a
// loud transport-level error instead.
func refuseCrossOriginRedirect(expectedHost string) func(*http.Request, []*http.Request) error {
	if expectedHost == "" {
		return nil
	}
	return func(req *http.Request, _ []*http.Request) error {
		if !strings.EqualFold(req.URL.Host, expectedHost) {
			return fmt.Errorf("%w: %s -> %s", ErrCrossOriginRedirect, expectedHost, req.URL.Host)
		}
		return nil
	}
}

func (c *Client) Capabilities() backend.Caps { return c.caps }

// urlFor builds the absolute URL for an /api/v2/... path. Nil query
// is allowed for parameterless endpoints.
func (c *Client) urlFor(path string, query url.Values) string {
	u := c.base + "/api/v2" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

// doGet executes a GET against the resolved URL and decodes the
// response into dst. Wraps low-level transport failures as
// ErrUnreachable, 401/403 as ErrUnauthorized, and 5xx/429 in a
// transientError that opts into the C1 backoff loop via Retryable().
// 4xx (other than 401/403) surfaces as a one-shot error.
func (c *Client) doGet(ctx context.Context, fullURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return c.exec(req, dst)
}

// doPost executes a POST with a JSON body and (optionally) decodes
// the JSON response. Mirrors doGet's error contract.
func (c *Client) doPost(ctx context.Context, fullURL string, body, dst any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return c.exec(req, dst)
}

// doDelete executes a DELETE and discards any response body. Same
// error contract as doGet.
func (c *Client) doDelete(ctx context.Context, fullURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fullURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	return c.exec(req, nil)
}

// exec runs a built request through the shared do -> classify ->
// drain-or-decode pipeline. Callers retain control of method, URL,
// body, and headers; everything after the request is built lives
// here so the three doX methods don't reimplement the error
// wrapping.
//
// dst == nil drains the body so the connection can be reused (the
// drain failure is intentionally swallowed — the response status
// has already been classified); otherwise the body is decoded as
// JSON into dst, matching the `Accept: application/json` the doX
// callers set.
func (c *Client) exec(req *http.Request, dst any) error {
	// gosec G704 (SSRF taint): the request URL is the user-configured
	// Alertmanager / Mimir endpoint by design — the whole purpose of
	// this tool is to HTTP to operator-supplied AM URLs. Host pinning
	// at the transport layer (parseExpectedHost in mimir.New) blocks
	// auth-replay across redirects to a different origin.
	resp, err := c.http.Do(req) //nolint:gosec // G704: Alertmanager URL is operator-configured by design
	if err != nil {
		return fmt.Errorf("%w: %w", backend.ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := c.classifyStatus(resp); err != nil {
		return err
	}

	if dst == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	limited := io.LimitReader(resp.Body, c.maxBodyBytes)
	if err := json.NewDecoder(limited).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	// Drain trailing bytes the decoder didn't consume so http.Transport
	// can return the connection to its idle pool. Go's body.Close
	// auto-drains only a small prefix; with a non-trivial alert payload
	// the trailing whitespace / chunked-framing past the closing `]`
	// pushes the body past that limit and Close terminates the conn
	// instead. At 15s polling × 10 tenants that's a fresh TCP/TLS
	// handshake on every tick.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// execHeaders runs a built request through the same do -> classify
// pipeline as exec but returns the response headers (with the body
// drained) instead of decoding JSON. Used by ProbeReadyAt to read
// the server's `Date` header without forcing every reader to grow
// a JSON-vs-headers branch in their decoder path.
//
// The body is drained so http.Transport can return the connection
// to its idle pool, mirroring exec's contract.
func (c *Client) execHeaders(req *http.Request) (http.Header, error) {
	// gosec G107: the URL is built by ProbeReadyAt from c.urlFor,
	// which composes the package's validated BaseURL with a fixed
	// path — there is no user-controlled URL component on this
	// codepath. Same pattern as exec() above.
	resp, err := c.http.Do(req) //nolint:gosec // URL composed from validated BaseURL + fixed path
	if err != nil {
		return nil, fmt.Errorf("%w: %w", backend.ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := c.classifyStatus(resp); err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Header, nil
}

// classifyStatus maps an HTTP status code to a backend sentinel or a
// transient error. Returns nil for 2xx. For 4xx codes that aren't
// 401/403/429 the body is read so the user sees the server's
// message (Alertmanager surfaces validation failures as plain-text
// 400 bodies). Reading consumes resp.Body — only safe to call on
// errors, where downstream JSON decoding would not run anyway.
//
// The body is server-controlled, so it is sanitised before
// landing in an error string. Without this, a hostile backend
// could embed ANSI escape sequences in a 400 response body and
// rewrite the operator's terminal title / cursor on every retry.
// cleanErrorBody strips control characters and caps the result at
// maxErrorBodyLen.
func (c *Client) classifyStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: HTTP %d", backend.ErrUnauthorized, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return &transientError{status: resp.StatusCode}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, c.maxBodyBytes))
	msg := cleanErrorBody(body)
	if resp.StatusCode == http.StatusNotFound {
		// Sentinel-first rendering matches the 401 idiom above, so
		// operators read "not found: HTTP 404[: body]" rather than
		// a trailing sentinel tail that adds no information.
		if msg == "" {
			return fmt.Errorf("%w: HTTP 404", backend.ErrNotFound)
		}
		return fmt.Errorf("%w: HTTP 404: %s", backend.ErrNotFound, msg)
	}
	if msg == "" {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
}

// maxErrorBodyLen caps the bytes of a server-supplied error body
// that land in a wrapped error. Long enough for an Alertmanager
// validation message ("missing matcher: alertname") and short
// enough that a multi-line stack trace doesn't flood the operator's
// flash strip.
const maxErrorBodyLen = 512

// cleanErrorBody sanitises a server-controlled response body so it
// is safe to embed in an error string / log line: control
// characters (incl. ANSI escapes, CR, LF, tab) become spaces, runs
// of whitespace collapse to single spaces, and the result is
// trimmed and capped at maxErrorBodyLen with a trailing ellipsis on
// truncation. Prevents ANSI-escape injection from a hostile backend
// rewriting the operator's terminal on every retry.
func cleanErrorBody(in []byte) string {
	out := make([]byte, 0, len(in))
	for _, b := range in {
		switch {
		case b < 0x20, b == 0x7f:
			out = append(out, ' ')
		default:
			out = append(out, b)
		}
	}
	collapsed := strings.Join(strings.Fields(string(out)), " ")
	if len(collapsed) > maxErrorBodyLen {
		collapsed = collapsed[:maxErrorBodyLen] + "…"
	}
	return collapsed
}

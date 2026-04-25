// SPDX-License-Identifier: Apache-2.0

// Package vanilla implements the backend.Client interface against
// the upstream Alertmanager v2 HTTP API. The Mimir wrapper composes
// a vanilla client with a prefix and tenant header (per audit §5.1)
// rather than maintaining a parallel implementation, so this package
// is the source of truth for "talk to /api/v2/...".
//
// Read endpoints (ListAlerts/AlertGroups/Silences/GetSilence/Receivers/
// Status) live in read.go; write endpoints (CreateSilence,
// UpdateSilence, ExpireSilence) land in #13. Capability-gated methods
// stay as ErrUnsupported stubs in this package — Mimir's wrapper
// implements them when caps allow, post-v0.1.
package vanilla

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// Compile-time assertion that the partial implementation in v0.1
// satisfies backend.Reader. The full backend.Client (including
// Writer) is asserted in #13 once the silence write paths land.
var _ backend.Reader = (*Client)(nil)

// Default request timeout when ClientConfig.Timeout is zero. Picked
// large enough for a slow backend with thousands of alerts but
// small enough that the polling loop does not stall on a hung
// request — the C1 backoff cap is poll_interval × 6, so a 30 s
// request timeout fits inside the smallest sensible poll_interval
// (1 m default).
const defaultRequestTimeout = 30 * time.Second

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
//   - Caps: capability flags from `a10r.yaml`. v0.1 leaves all flags
//     off on vanilla; the field exists so the Mimir wrapper can pass
//     real caps through.
type ClientConfig struct {
	BaseURL   string
	Prefix    string
	Transport http.RoundTripper
	Timeout   time.Duration
	Caps      backend.Caps
}

// Client is the vanilla Alertmanager v2 backend. Constructed via
// New; safe for concurrent use across goroutines.
type Client struct {
	base string
	http *http.Client
	caps backend.Caps
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
		base: cfg.BaseURL + cfg.Prefix,
		http: &http.Client{Transport: transport, Timeout: timeout},
		caps: cfg.Caps,
	}, nil
}

// Capabilities returns the caps the client was constructed with.
func (c *Client) Capabilities() backend.Caps { return c.caps }

// urlFor builds the absolute URL for an /api/v2/... path. query may
// be nil for parameterless endpoints.
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

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", backend.ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := classifyStatus(resp); err != nil {
		return err
	}

	if dst == nil {
		// Drain the body so the connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// classifyStatus maps an HTTP status code to a backend sentinel or a
// transient error. Returns nil for 2xx.
func classifyStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: HTTP %d", backend.ErrUnauthorized, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return &transientError{status: resp.StatusCode}
	}
	return fmt.Errorf("unexpected HTTP %d", resp.StatusCode)
}

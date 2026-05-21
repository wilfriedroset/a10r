// SPDX-License-Identifier: Apache-2.0

// Package transport composes http.RoundTripper layers for backend
// auth, header injection, TLS, and proxy configuration. The split
// into a transport package (rather than baking into the vanilla /
// Mimir clients) reflects the principle that auth and tenant
// scoping are orthogonal concerns: every backend type uses the same
// auth shapes (basic / authorization / bearer — mTLS, OAuth2, and
// SigV4 are deferred per ADR 0029, slotting in as additional
// RoundTripper layers when they land), and Mimir only differs from
// vanilla in needing the tenant header injected.
//
// Schema mirrors the Prometheus `remote_write` block — `basic_auth:`,
// `authorization:`, `bearer_token:` are peers on the Backend struct
// rather than a discriminated `auth:` envelope.
package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/wilfriedroset/a10r/internal/config"
)

// Header names used by built-in auth types. The literal strings are
// also wire-level, so renaming a constant is a wire break — pinned
// in the test suite.
const (
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

// Errors returned by NewAuth and NewBase for malformed inputs.
// Validation is eager so the factory surfaces the misconfiguration at
// startup rather than on the first poll.
var (
	ErrMissingBasicCreds  = errors.New("basic_auth requires both username and password")
	ErrMissingBearerToken = errors.New("bearer_token must not be empty")
	ErrMissingAuthCreds   = errors.New("authorization requires credentials")
	ErrInvalidProxyURL    = errors.New("proxy_url is not a valid URL")
	ErrInvalidCABundle    = errors.New("tls_config.ca is not a valid PEM bundle")
)

// AuthOptions bundles the three peer auth blocks that may appear on
// a Backend. The constructor enforces an "at most one" rule
// (matching Prometheus's HTTPClientConfig); passing a second
// non-nil block alongside a populated one returns an error rather
// than silently picking a winner.
//
// The struct shape mirrors Prometheus's HTTPClientConfig peers so
// the factory can copy fields straight off `config.Backend` without
// translation.
//
// ExpectedHost is the host portion of the configured BaseURL (as
// parsed by url.URL.Host). When set, the auth RoundTripper only
// injects credentials on requests whose req.URL.Host matches —
// defends against credential replay on cross-origin redirects: a
// hostile / hijacked backend that returns `302 Location:
// https://attacker/` cannot replay the Authorization header on the
// redirect target. Empty ExpectedHost preserves the unrestricted
// legacy behaviour for tests.
type AuthOptions struct {
	BasicAuth     *config.BasicAuth
	Authorization *config.Authorization
	BearerToken   string
	ExpectedHost  string
}

// NewAuth returns a RoundTripper that wraps base with the auth
// scheme described in opts. A zero-value AuthOptions is a no-op and
// returns base unchanged.
//
// nil base defaults to http.DefaultTransport — keeps test wiring
// terse and matches the stdlib convention used by http.Client.
func NewAuth(opts AuthOptions, base http.RoundTripper) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	switch {
	case opts.BasicAuth != nil:
		return newBasic(opts.BasicAuth, opts.ExpectedHost, base)
	case opts.Authorization != nil:
		return newAuthorization(opts.Authorization, opts.ExpectedHost, base)
	case opts.BearerToken != "":
		return newBearer(opts.BearerToken, opts.ExpectedHost, base)
	default:
		return base, nil
	}
}

// WithHeaders wraps base in a RoundTripper that injects every
// (name, value) pair from headers on outgoing requests. nil or
// empty headers short-circuits to base unchanged.
//
// Reserved-header validation lives in the config layer (see
// config.validateHeaders) so this layer can trust whatever it
// receives. Order of injection across many headers is not specified;
// callers must not rely on it.
//
// Equivalent to WithHostPinnedHeaders(base, headers, "") — leaves
// the headers RT unrestricted, matching the legacy behaviour for
// existing tests. Production callers should use the pinned variant
// so a hijacked redirect target does not see the headers.
func WithHeaders(base http.RoundTripper, headers map[string]string) http.RoundTripper {
	return WithHostPinnedHeaders(base, headers, "")
}

// WithHostPinnedHeaders is the host-pinned variant of WithHeaders:
// when expectedHost is non-empty, the headers (which include the
// tenant identifier on Mimir setups) are only injected on requests
// whose req.URL.Host matches. A hijacked backend that responds 302
// to attacker.example never sees the X-Scope-OrgID / arbitrary
// auth-bearing headers a configured backend would have sent.
func WithHostPinnedHeaders(base http.RoundTripper, headers map[string]string, expectedHost string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if len(headers) == 0 {
		return base
	}
	// Snapshot the map so a later mutation by the caller cannot leak
	// into in-flight requests.
	snap := make(map[string]string, len(headers))
	maps.Copy(snap, headers)
	return &headersRT{base: base, headers: snap, expectedHost: expectedHost}
}

// WithUserAgent wraps base in a RoundTripper that sets the User-Agent
// header on every outgoing request per RFC 9110 §10.1.5. Overrides
// any User-Agent the caller already set so backends can rely on a
// consistent value identifying the a10r build that issued the
// request.
//
// An empty ua short-circuits to base unchanged so the wiring layer
// can pass an unset value (e.g. dev-build with the build vars
// stripped) without conditional plumbing.
func WithUserAgent(base http.RoundTripper, ua string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if ua == "" {
		return base
	}
	return &userAgentRT{base: base, ua: ua}
}

// BaseOptions bundles every TLS / proxy knob plumbed through to the
// underlying *http.Transport. Each field maps 1:1 onto the
// equivalent `config.Backend` / `config.TLSConfig` field; the
// translation is in the factory layer so the shape is stable across
// future schema additions.
type BaseOptions struct {
	TLS                  *config.TLSConfig
	ProxyURL             string
	NoProxy              string
	ProxyFromEnvironment bool
}

// NewBase returns the *http.Transport that sits at the bottom of
// every backend's roundtripper chain. Returns http.DefaultTransport
// unchanged when no TLS or proxy configuration is requested so the
// stdlib's connection pool defaults apply.
//
// Callers wrap the returned transport with NewAuth, WithHeaders, and
// WithUserAgent in that order — auth is innermost so a downstream
// proxy that strips Authorization still sees the User-Agent.
func NewBase(opts BaseOptions) (http.RoundTripper, error) {
	if opts.TLS == nil && opts.ProxyURL == "" && opts.NoProxy == "" && !opts.ProxyFromEnvironment {
		return http.DefaultTransport, nil
	}
	// Clone to inherit the stdlib defaults (HTTP/2, idle timeouts,
	// dialer settings) without mutating the global default.
	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("http.DefaultTransport is not *http.Transport — cannot clone for per-backend customisation")
	}
	t := dt.Clone()

	if opts.TLS != nil {
		cfg, err := buildTLSConfig(opts.TLS)
		if err != nil {
			return nil, err
		}
		t.TLSClientConfig = cfg
	}

	proxyFunc, err := buildProxyFunc(opts)
	if err != nil {
		return nil, err
	}
	if proxyFunc != nil {
		t.Proxy = proxyFunc
	}
	return t, nil
}

func newBasic(spec *config.BasicAuth, expectedHost string, base http.RoundTripper) (http.RoundTripper, error) {
	if spec.Username == "" || spec.Password == "" {
		return nil, ErrMissingBasicCreds
	}
	return &basicRT{base: base, user: spec.Username, pass: spec.Password, expectedHost: expectedHost}, nil
}

func newBearer(token, expectedHost string, base http.RoundTripper) (http.RoundTripper, error) {
	if token == "" {
		return nil, ErrMissingBearerToken
	}
	return &addHeaderRT{base: base, name: headerAuthorization, value: bearerPrefix + token, expectedHost: expectedHost}, nil
}

func newAuthorization(spec *config.Authorization, expectedHost string, base http.RoundTripper) (http.RoundTripper, error) {
	if spec.Credentials == "" {
		return nil, ErrMissingAuthCreds
	}
	t := spec.Type
	if t == "" {
		t = config.DefaultAuthorizationType
	}
	return &addHeaderRT{base: base, name: headerAuthorization, value: t + " " + spec.Credentials, expectedHost: expectedHost}, nil
}

func buildTLSConfig(spec *config.TLSConfig) (*tls.Config, error) {
	// Surface the two dangerous TLS knobs at the moment they take
	// effect rather than relying on the runTUI startup INFO: any
	// programmatic caller (tests, future REPL, embedding library)
	// that wires NewBase directly bypasses the config-load logging
	// surface. slog.Default() is the shared sink so this layer needs
	// no constructor seam — the warning is the operator-actionable
	// signal.
	if spec.InsecureSkipVerify {
		slog.Warn("TLS certificate verification disabled — MITM possible")
	}
	cfg := &tls.Config{
		ServerName:         spec.ServerName,
		InsecureSkipVerify: spec.InsecureSkipVerify, //nolint:gosec // user opt-in: tls_config.insecure_skip_verify is documented in the schema
	}
	if spec.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(spec.CA)) {
			return nil, ErrInvalidCABundle
		}
		cfg.RootCAs = pool
		// Inline CA REPLACES the system root pool for this backend
		// (Prometheus parity); the trust narrowing is surprising
		// for callers reading "added a CA" as "augmented" rather
		// than "replaced". Only inline is supported today, so
		// ca_source is hard-coded; broaden the attr when the file /
		// ref variants land (same reservation posture as ADR 0029).
		slog.Warn("custom CA bundle replaces system roots", slog.String("ca_source", "inline"))
	}
	if v, ok := tlsVersionLookup(spec.MinVersion); ok {
		cfg.MinVersion = v
	}
	if v, ok := tlsVersionLookup(spec.MaxVersion); ok {
		cfg.MaxVersion = v
	}
	return cfg, nil
}

// tlsVersionLookup maps the wire-level strings Prometheus accepts
// (and the schema validates) to the stdlib uint16 constants. The
// second return value distinguishes "user did not configure" (no
// override) from a configured version.
func tlsVersionLookup(s string) (uint16, bool) {
	switch s {
	case "TLS10":
		return tls.VersionTLS10, true
	case "TLS11":
		return tls.VersionTLS11, true
	case "TLS12":
		return tls.VersionTLS12, true
	case "TLS13":
		return tls.VersionTLS13, true
	default:
		return 0, false
	}
}

// buildProxyFunc translates the proxy fields into the http.Transport
// Proxy callback. Returns nil when no proxy override is requested so
// the cloned transport keeps DefaultTransport's behaviour. The schema
// layer (config.Backend.validateProxy) guarantees proxy_url and
// proxy_from_environment are not both set, so this function only
// branches between them.
func buildProxyFunc(opts BaseOptions) (func(*http.Request) (*url.URL, error), error) {
	if opts.ProxyFromEnvironment {
		return http.ProxyFromEnvironment, nil
	}
	if opts.ProxyURL == "" {
		return nil, nil //nolint:nilnil // explicit "no proxy override" — caller checks for nil function
	}
	parsed, err := url.Parse(opts.ProxyURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidProxyURL, opts.ProxyURL, err)
	}
	matcher := compileNoProxy(opts.NoProxy)
	return func(req *http.Request) (*url.URL, error) {
		if matcher != nil && matcher(req.URL.Host) {
			return nil, nil //nolint:nilnil // matched a no_proxy entry — caller treats nil URL as "go direct"
		}
		return parsed, nil
	}, nil
}

// compileNoProxy parses a Prometheus-style `no_proxy` directive
// into a host-matching predicate. Entries are comma-separated; an
// entry starting with `.` is a suffix match against the host
// (`.svc.cluster.local` matches `foo.svc.cluster.local`), every
// other entry is an exact match against the host (port stripped).
// Empty or whitespace-only input returns nil so the caller can
// short-circuit.
//
// CIDR matching (a Prometheus extension) is intentionally out of
// scope for this iteration — it requires resolving the request's
// destination IP, which is more orchestration than a TUI poller
// warrants. Callers who need CIDR-based exclusion should rely on
// `proxy_from_environment: true` and the OS-level NO_PROXY parser.
func compileNoProxy(spec string) func(host string) bool {
	if spec == "" {
		return nil
	}
	var exact []string
	var suffixes []string
	for _, raw := range splitComma(spec) {
		switch {
		case raw == "":
			continue
		case raw[0] == '.':
			suffixes = append(suffixes, raw)
		default:
			exact = append(exact, raw)
		}
	}
	if len(exact) == 0 && len(suffixes) == 0 {
		return nil
	}
	return func(host string) bool {
		// Strip the port — the user writes "localhost", not
		// "localhost:9093".
		if i := strings.LastIndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if slices.Contains(exact, host) {
			return true
		}
		for _, s := range suffixes {
			if strings.HasSuffix(host, s) {
				return true
			}
		}
		return false
	}
}

// splitComma trims leading/trailing whitespace on each fragment.
// Empty entries are kept (the caller drops them) so the function
// is usable for non-no_proxy callers as well.
func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// basicRT injects HTTP Basic auth via http.Request.SetBasicAuth so
// the base64 encoding lives in stdlib rather than this package.
//
// expectedHost is the host portion of the originally-configured
// BaseURL. When non-empty, basicRT skips injection on any request
// targeted at a different host so a redirect chain cannot replay
// the credentials at an attacker-controlled origin.
type basicRT struct {
	base         http.RoundTripper
	user, pass   string
	expectedHost string
}

func (rt *basicRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if hostMatches(rt.expectedHost, cloned.URL.Host) {
		cloned.SetBasicAuth(rt.user, rt.pass)
	}
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}

// addHeaderRT sets a single (name, value) header on every request.
// Used both for bearer auth (with name=Authorization, value=Bearer
// <token>) and for the generic Authorization spec.
//
// expectedHost has the same semantics as basicRT.expectedHost —
// non-empty means "only inject when req.URL.Host matches".
type addHeaderRT struct {
	base         http.RoundTripper
	name, value  string
	expectedHost string
}

func (rt *addHeaderRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if hostMatches(rt.expectedHost, cloned.URL.Host) {
		cloned.Header.Set(rt.name, rt.value)
	}
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}

// headersRT applies a fixed set of headers to every request. Distinct
// from addHeaderRT because callers commonly want a multi-key map and
// chaining N addHeaderRTs would be O(N) Clone calls per request.
//
// expectedHost gates injection the same way basicRT / addHeaderRT
// do — the tenant header / arbitrary auth-bearing headers must
// not leak across redirects.
type headersRT struct {
	base         http.RoundTripper
	headers      map[string]string
	expectedHost string
}

func (rt *headersRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if hostMatches(rt.expectedHost, cloned.URL.Host) {
		for k, v := range rt.headers {
			cloned.Header.Set(k, v)
		}
	}
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}

// hostMatches reports whether reqHost belongs to the expected
// origin. Empty expected = "no pinning configured" so the caller
// behaves as the unrestricted legacy path (always inject) — used
// by tests and the legacy WithHeaders path. Non-empty performs a
// case-insensitive comparison; the http.Request.URL.Host fields
// already include any port, and the constructor receives whatever
// url.URL.Host produced from the configured BaseURL, so the two
// strings line up.
func hostMatches(expected, reqHost string) bool {
	if expected == "" {
		return true
	}
	return strings.EqualFold(expected, reqHost)
}

// userAgentRT injects a fixed User-Agent on every request. Distinct
// from addHeaderRT only because Go's http stack auto-populates
// Header["User-Agent"] from req.Header rather than synthesising a
// default — Set unconditionally overrides any caller-supplied value.
type userAgentRT struct {
	base http.RoundTripper
	ua   string
}

func (rt *userAgentRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("User-Agent", rt.ua)
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}

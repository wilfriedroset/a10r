// SPDX-License-Identifier: Apache-2.0

// Package transport composes http.RoundTripper layers for backend
// auth, header injection, TLS, and proxy configuration. mTLS, OAuth2,
// and SigV4 are deferred per ADR 0029, slotting in as further layers.
// Schema mirrors the Prometheus remote_write block: basic_auth,
// authorization, and bearer_token are peers on the Backend struct.
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

// These literals are wire-level: renaming a constant is a wire break.
const (
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

// Validation is eager so the factory surfaces misconfiguration at
// startup rather than on the first poll.
var (
	ErrMissingBasicCreds  = errors.New("basic_auth requires both username and password")
	ErrMissingBearerToken = errors.New("bearer_token must not be empty")
	ErrMissingAuthCreds   = errors.New("authorization requires credentials")
	ErrInvalidProxyURL    = errors.New("proxy_url is not a valid URL")
	ErrInvalidCABundle    = errors.New("tls_config.ca is not a valid PEM bundle")
)

// AuthOptions bundles the peer auth blocks; the constructor enforces
// "at most one" (matching Prometheus's HTTPClientConfig).
//
// ExpectedHost, when set, restricts credential injection to requests
// whose req.URL.Host matches — defends against credential replay on
// cross-origin redirects: a hijacked backend returning 302 Location:
// https://attacker/ cannot replay the Authorization header on the
// redirect target. Empty preserves the unrestricted legacy behaviour.
type AuthOptions struct {
	BasicAuth     *config.BasicAuth
	Authorization *config.Authorization
	BearerToken   string
	ExpectedHost  string
}

// NewAuth wraps base with the auth scheme in opts. A zero-value
// AuthOptions is a no-op; nil base defaults to http.DefaultTransport.
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

// WithHeaders injects every (name, value) pair from headers on
// outgoing requests. Reserved-header validation lives in the config
// layer (config.validateHeaders), so this layer trusts its input.
// Unrestricted (no host pinning); production callers should prefer
// WithHostPinnedHeaders so a hijacked redirect cannot see the headers.
func WithHeaders(base http.RoundTripper, headers map[string]string) http.RoundTripper {
	return WithHostPinnedHeaders(base, headers, "")
}

// WithHostPinnedHeaders is the host-pinned variant of WithHeaders:
// non-empty expectedHost restricts injection to matching req.URL.Host
// so a hijacked redirect never sees the tenant / auth-bearing headers.
func WithHostPinnedHeaders(base http.RoundTripper, headers map[string]string, expectedHost string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if len(headers) == 0 {
		return base
	}
	// Snapshot so later caller mutation cannot leak into in-flight requests.
	snap := make(map[string]string, len(headers))
	maps.Copy(snap, headers)
	return &headersRT{base: base, headers: snap, expectedHost: expectedHost}
}

// WithUserAgent sets the User-Agent header (RFC 9110 §10.1.5),
// overriding any caller-supplied value so backends see a consistent
// a10r identifier. Empty ua short-circuits to base unchanged so the
// wiring layer can pass a stripped dev-build value unconditionally.
func WithUserAgent(base http.RoundTripper, ua string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if ua == "" {
		return base
	}
	return &userAgentRT{base: base, ua: ua}
}

// BaseOptions bundles the TLS / proxy knobs plumbed through to the
// underlying *http.Transport.
type BaseOptions struct {
	TLS                  *config.TLSConfig
	ProxyURL             string
	NoProxy              string
	ProxyFromEnvironment bool
}

// NewBase returns the *http.Transport at the bottom of a backend's
// roundtripper chain, or http.DefaultTransport unchanged when no TLS
// or proxy is requested. Callers wrap it NewAuth, WithHeaders,
// WithUserAgent in that order — auth innermost so a downstream proxy
// that strips Authorization still sees the User-Agent.
func NewBase(opts BaseOptions) (http.RoundTripper, error) {
	if opts.TLS == nil && opts.ProxyURL == "" && opts.NoProxy == "" && !opts.ProxyFromEnvironment {
		return http.DefaultTransport, nil
	}
	// Clone to inherit stdlib defaults without mutating the global default.
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
	// Warn where the knob takes effect, not at startup: a programmatic
	// caller wiring NewBase directly bypasses the config-load logging.
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
		// (Prometheus parity), not augments it — surprising trust
		// narrowing. ca_source is hard-coded until file/ref variants
		// land (same reservation posture as ADR 0029).
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

const (
	tlsVersionTLS10 = "TLS10"
	tlsVersionTLS11 = "TLS11"
	tlsVersionTLS12 = "TLS12"
	tlsVersionTLS13 = "TLS13"
)

// tlsVersionLookup maps the wire-level strings to stdlib uint16
// constants. The ok return distinguishes "not configured" from a set
// version.
func tlsVersionLookup(s string) (uint16, bool) {
	switch s {
	case tlsVersionTLS10:
		return tls.VersionTLS10, true
	case tlsVersionTLS11:
		return tls.VersionTLS11, true
	case tlsVersionTLS12:
		return tls.VersionTLS12, true
	case tlsVersionTLS13:
		return tls.VersionTLS13, true
	default:
		return 0, false
	}
}

// buildProxyFunc builds the http.Transport Proxy callback, or nil for
// no override. config.Backend.validateProxy guarantees proxy_url and
// proxy_from_environment are not both set, so this only branches.
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

// compileNoProxy parses a Prometheus-style no_proxy directive into a
// host predicate (comma-separated; a leading "." is a suffix match,
// otherwise exact), returning nil for empty input.
//
// CIDR matching is intentionally out of scope: it needs the request's
// destination IP, more orchestration than a TUI poller warrants. Use
// proxy_from_environment and the OS-level NO_PROXY parser instead.
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
		return matchNoProxy(exact, suffixes, host)
	}
}

// matchNoProxy reports whether host matches a no_proxy entry. The port
// is stripped first: the user writes "localhost", not "localhost:9093".
func matchNoProxy(exact, suffixes []string, host string) bool {
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

// splitComma trims whitespace per fragment; empty entries are kept for
// the caller to drop, keeping the helper reusable.
func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// basicRT injects HTTP Basic auth. Non-empty expectedHost skips
// injection on a mismatched host so a redirect chain cannot replay
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

// addHeaderRT sets a single header on every request (bearer and
// generic Authorization). expectedHost gates injection as in basicRT.
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

// headersRT applies a fixed header map, distinct from addHeaderRT to
// avoid O(N) Clone calls from chaining N single-header RTs.
// expectedHost gates injection as in basicRT, so tenant / auth headers
// do not leak across redirects.
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

// hostMatches reports whether reqHost belongs to the expected origin.
// Empty expected means no pinning configured (always inject), the
// unrestricted legacy path. Both sides carry any port verbatim, so the
// case-insensitive comparison lines up.
func hostMatches(expected, reqHost string) bool {
	if expected == "" {
		return true
	}
	return strings.EqualFold(expected, reqHost)
}

// userAgentRT injects a fixed User-Agent, unconditionally overriding
// the value Go's http stack would otherwise auto-populate.
type userAgentRT struct {
	base http.RoundTripper
	ua   string
}

func (rt *userAgentRT) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("User-Agent", rt.ua)
	return rt.base.RoundTrip(cloned) //nolint:wrapcheck // RoundTripper contract: errors propagate as-is
}

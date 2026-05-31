// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

// captureSlogDefault swaps slog.Default() for a text-handler writing
// into the returned strings.Builder for the duration of t. Used by
// the buildTLSConfig warning tests so any caller (programmatic or
// production) gets a visible signal when a dangerous TLS knob is
// enabled. Tests using this helper run sequentially because the swap
// is process-wide — mirrors the convention in
// internal/tui/page/silences/silences_test.go's newAuditLogBuf.
func captureSlogDefault(t *testing.T) *strings.Builder {
	t.Helper()
	buf := &strings.Builder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// captureHandler is an http.HandlerFunc that records the headers of
// the last request it served, so tests can assert wire-level shape.
type captureHandler struct {
	headers http.Header
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.headers = r.Header.Clone()
	w.WriteHeader(http.StatusNoContent)
}

// roundTripOnce drives rt through one GET request against srv. Tests
// inspect the resulting wire headers via a captureHandler attached
// to srv rather than the roundtripped request, so this helper is
// deliberately void-returning.
func roundTripOnce(t *testing.T, rt http.RoundTripper, srv *httptest.Server) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
}

func TestNewAuth_EmptyOptionsReturnsBase(t *testing.T) {
	t.Parallel()

	base := http.DefaultTransport
	rt, err := NewAuth(AuthOptions{}, base)
	require.NoError(t, err)
	require.Same(t, base, rt, "zero-value auth must return the base RT unchanged")
}

func TestNewAuth_NilBaseDefaultsToDefaultTransport(t *testing.T) {
	t.Parallel()

	rt, err := NewAuth(AuthOptions{}, nil)
	require.NoError(t, err)
	require.Same(t, http.DefaultTransport, rt,
		"nil base + zero-value auth must return http.DefaultTransport unchanged")
}

func TestNewAuth_HeadersOnWire(t *testing.T) {
	t.Parallel()

	// Each case wires one auth scheme through NewAuth, drives a real
	// round-trip, and asserts the Authorization header captured server-
	// side. The Authorization rows cover the Type-defaults-to-Bearer
	// (Prometheus parity) and custom-Type (Token, GenieKey, etc.) paths.
	cases := []struct {
		name string
		opts AuthOptions
		want string
	}{
		{
			name: "basic auth",
			opts: AuthOptions{BasicAuth: &config.BasicAuth{Username: "alice", Password: "s3cret"}},
			want: "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret")),
		},
		{
			name: "bearer token",
			opts: AuthOptions{BearerToken: "abc.def.ghi"},
			want: "Bearer abc.def.ghi",
		},
		{
			name: "authorization default type is bearer",
			opts: AuthOptions{Authorization: &config.Authorization{Credentials: "tok"}},
			want: "Bearer tok",
		},
		{
			name: "authorization custom type",
			opts: AuthOptions{Authorization: &config.Authorization{Type: "Token", Credentials: "abcdef"}},
			want: "Token abcdef",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srvCap := &captureHandler{}
			srv := httptest.NewServer(srvCap)
			t.Cleanup(srv.Close)

			rt, err := NewAuth(tc.opts, http.DefaultTransport)
			require.NoError(t, err)

			roundTripOnce(t, rt, srv)

			require.Equal(t, tc.want, srvCap.headers.Get(headerAuthorization))
		})
	}
}

func TestNewAuth_MalformedInputsReturnSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		opts    AuthOptions
		wantErr error
	}{
		{
			name:    "basic missing password",
			opts:    AuthOptions{BasicAuth: &config.BasicAuth{Username: "u"}},
			wantErr: ErrMissingBasicCreds,
		},
		{
			name:    "basic missing username",
			opts:    AuthOptions{BasicAuth: &config.BasicAuth{Password: "p"}},
			wantErr: ErrMissingBasicCreds,
		},
		{
			name:    "authorization missing credentials",
			opts:    AuthOptions{Authorization: &config.Authorization{Type: "Bearer"}},
			wantErr: ErrMissingAuthCreds,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewAuth(tc.opts, http.DefaultTransport)
			require.Error(t, err)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestWithHeaders_InjectsEveryEntry(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt := WithHeaders(http.DefaultTransport, map[string]string{
		"X-Scope-OrgID":   "tenant-a",
		"X-Gateway-Token": "g1",
	})
	roundTripOnce(t, rt, srv)

	require.Equal(t, "tenant-a", srvCap.headers.Get("X-Scope-Orgid"))
	require.Equal(t, "g1", srvCap.headers.Get("X-Gateway-Token"))
}

func TestWithHeaders_SnapshotsTheMap(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	in := map[string]string{"X-Snap": "before"}
	rt := WithHeaders(http.DefaultTransport, in)
	in["X-Snap"] = "after"
	in["X-Late"] = "added"

	roundTripOnce(t, rt, srv)

	require.Equal(t, "before", srvCap.headers.Get("X-Snap"),
		"caller's later mutation must not leak into in-flight requests")
	require.Empty(t, srvCap.headers.Get("X-Late"),
		"caller's later additions must not leak either")
}

func TestComposition_AuthAndHeadersBothApply(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	authed, err := NewAuth(AuthOptions{BearerToken: "tok"}, http.DefaultTransport)
	require.NoError(t, err)

	rt := WithHeaders(authed, map[string]string{"X-Scope-OrgID": "tenant-b"})
	roundTripOnce(t, rt, srv)

	require.Equal(t, "Bearer tok", srvCap.headers.Get(headerAuthorization),
		"auth header must reach the wire")
	require.Equal(t, "tenant-b", srvCap.headers.Get("X-Scope-Orgid"),
		"tenant header must reach the wire")
}

func TestWithUserAgent_Injects(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt := WithUserAgent(http.DefaultTransport, "a10r/1.2.3")
	roundTripOnce(t, rt, srv)

	require.Equal(t, "a10r/1.2.3", srvCap.headers.Get("User-Agent"))
}

func TestWithUserAgent_OverridesCallerSetUA(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt := WithUserAgent(http.DefaultTransport, "a10r/1.2.3")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "should-be-overridden/9.9")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)

	require.Equal(t, "a10r/1.2.3", srvCap.headers.Get("User-Agent"))
}

func TestRoundTrip_DoesNotMutateCallerRequest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	rt, err := NewAuth(AuthOptions{BearerToken: "tok"}, http.DefaultTransport)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	// The bearer header must NOT be visible on the original request
	// — clone-then-mutate is the contract.
	require.Empty(t, req.Header.Get(headerAuthorization),
		"caller's request must not be mutated by the RoundTripper chain")
}

func TestNewBase_NoOptionsReturnsDefaultTransport(t *testing.T) {
	t.Parallel()

	rt, err := NewBase(BaseOptions{})
	require.NoError(t, err)
	require.Same(t, http.DefaultTransport, rt,
		"zero-value BaseOptions must short-circuit to http.DefaultTransport")
}

func TestNewBase_AppliesTLSConfig(t *testing.T) {
	t.Parallel()

	pem := generateSelfSignedCA(t)

	rt, err := NewBase(BaseOptions{
		TLS: &config.TLSConfig{
			CA:                 pem,
			ServerName:         "am.internal",
			InsecureSkipVerify: false,
			MinVersion:         "TLS12",
			MaxVersion:         "TLS13",
		},
	})
	require.NoError(t, err)

	// rt is a cloned *http.Transport — assert each TLS field round-tripped.
	tt, ok := rt.(*http.Transport)
	require.True(t, ok, "NewBase must return *http.Transport when TLS is configured")
	require.NotNil(t, tt.TLSClientConfig)
	require.Equal(t, "am.internal", tt.TLSClientConfig.ServerName)
	require.False(t, tt.TLSClientConfig.InsecureSkipVerify)
	require.Equal(t, uint16(tls.VersionTLS12), tt.TLSClientConfig.MinVersion)
	require.Equal(t, uint16(tls.VersionTLS13), tt.TLSClientConfig.MaxVersion)
	require.NotNil(t, tt.TLSClientConfig.RootCAs)
}

func TestNewBase_RejectsInvalidCABundle(t *testing.T) {
	t.Parallel()

	_, err := NewBase(BaseOptions{
		TLS: &config.TLSConfig{CA: "not a pem block"},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidCABundle)
}

func TestNewBase_AppliesProxyURL(t *testing.T) {
	t.Parallel()

	rt, err := NewBase(BaseOptions{ProxyURL: "http://proxy.internal:3128"})
	require.NoError(t, err)

	tt, ok := rt.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tt.Proxy)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://am.internal/api/v2/alerts", http.NoBody)
	require.NoError(t, err)
	got, err := tt.Proxy(req)
	require.NoError(t, err)
	require.Equal(t, "http://proxy.internal:3128", got.String())
}

func TestNewBase_NoProxyMatchesExactAndSuffix(t *testing.T) {
	t.Parallel()

	rt, err := NewBase(BaseOptions{
		ProxyURL: "http://proxy.internal:3128",
		NoProxy:  "localhost,127.0.0.1,.svc.cluster.local",
	})
	require.NoError(t, err)
	tt := rt.(*http.Transport)

	cases := []struct {
		host    string
		bypass  bool
		comment string
	}{
		{host: "localhost:9093", bypass: true, comment: "exact (with port stripped)"},
		{host: "127.0.0.1:80", bypass: true, comment: "exact IPv4"},
		{host: "alertmanager.svc.cluster.local:9093", bypass: true, comment: "suffix match"},
		{host: "am.example.com", bypass: false, comment: "no match — must use proxy"},
	}

	for _, tc := range cases {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+tc.host+"/api/v2/alerts", http.NoBody)
		require.NoError(t, err)
		got, err := tt.Proxy(req)
		require.NoError(t, err)
		if tc.bypass {
			require.Nil(t, got, "host %q must bypass proxy (%s)", tc.host, tc.comment)
			continue
		}
		require.NotNil(t, got, "host %q must route through proxy", tc.host)
	}
}

func TestNewBase_ProxyFromEnvironment(t *testing.T) {
	t.Parallel()

	rt, err := NewBase(BaseOptions{ProxyFromEnvironment: true})
	require.NoError(t, err)
	tt, ok := rt.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tt.Proxy, "proxy_from_environment must wire http.ProxyFromEnvironment")
}

func TestNewBase_RejectsInvalidProxyURL(t *testing.T) {
	t.Parallel()

	_, err := NewBase(BaseOptions{ProxyURL: "://nope"})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidProxyURL)
}

// generateSelfSignedCA emits a single-cert PEM bundle so TLS tests can
// drive the CA-bundle path without checking a fixture into the repo.
// The CA is regenerated per test run; the cert is never trusted by
// any real system — its only purpose is to satisfy
// x509.NewCertPool().AppendCertsFromPEM.
func generateSelfSignedCA(t *testing.T) string {
	t.Helper()
	// Hand-rolled because the real CA-generation helpers (crypto/tls,
	// x509) need 50+ lines of boilerplate. A pre-generated PEM works
	// too but the cert would expire over time. Use a stdlib
	// self-signed cert generated once and embedded in the test for
	// clarity.
	const pem = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----
`
	// Sanity-check this PEM is parseable so a future paste mistake
	// surfaces here rather than in the production code path.
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM([]byte(pem)),
		"embedded PEM must parse; regenerate generateSelfSignedCA's literal if this fails")
	return pem
}

// TestNewAuth_BasicAuth_HostPinDropsOnMismatch exercises the
// host-pin guard: when AuthOptions.ExpectedHost is set, the
// basic-auth RT must skip the SetBasicAuth call on any request whose req.URL.Host
// doesn't match. This is the single most important regression test
// in the package — the prior behaviour replayed credentials onto
// an attacker-controlled redirect target.
func TestNewAuth_BasicAuth_HostPinDropsOnMismatch(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt, err := NewAuth(AuthOptions{
		BasicAuth:    &config.BasicAuth{Username: "alice", Password: "s3cret"},
		ExpectedHost: "configured.example:9093",
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv) // srv.URL.Host is 127.0.0.1:<port>, never "configured.example:9093"

	require.Empty(t, srvCap.headers.Get(headerAuthorization),
		"basic auth must NOT be injected when req.URL.Host differs from ExpectedHost")
}

// TestNewAuth_BasicAuth_HostPinAppliesOnMatch is the positive
// counterpart: when the expected host equals the request host
// (case-insensitive), the basic-auth RT continues to inject Authorization.
func TestNewAuth_BasicAuth_HostPinAppliesOnMatch(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	rt, err := NewAuth(AuthOptions{
		BasicAuth:    &config.BasicAuth{Username: "alice", Password: "s3cret"},
		ExpectedHost: srvURL.Host,
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv)

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	require.Equal(t, want, srvCap.headers.Get(headerAuthorization),
		"matching ExpectedHost must inject Authorization unchanged")
}

// TestNewAuth_Bearer_HostPinDropsOnMismatch covers the same
// host-pin guard for the bearer token RoundTripper: a hostile
// redirect target must never see the bearer token.
func TestNewAuth_Bearer_HostPinDropsOnMismatch(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt, err := NewAuth(AuthOptions{
		BearerToken:  "abc.def.ghi",
		ExpectedHost: "configured.example:9093",
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv)

	require.Empty(t, srvCap.headers.Get(headerAuthorization),
		"bearer token must NOT be injected when req.URL.Host differs from ExpectedHost")
}

// TestNewAuth_Authorization_HostPinDropsOnMismatch covers the
// generic Authorization spec (custom Type) — the third
// credential-bearing RoundTripper that must not leak across
// origins.
func TestNewAuth_Authorization_HostPinDropsOnMismatch(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt, err := NewAuth(AuthOptions{
		Authorization: &config.Authorization{Type: "Token", Credentials: "abcdef"},
		ExpectedHost:  "configured.example:9093",
	}, http.DefaultTransport)
	require.NoError(t, err)

	roundTripOnce(t, rt, srv)

	require.Empty(t, srvCap.headers.Get(headerAuthorization),
		"Authorization header must NOT be injected when req.URL.Host differs from ExpectedHost")
}

// TestWithHostPinnedHeaders_DropsOnMismatch pins the headers-RT
// host guard: the tenant header (and any user-supplied
// auth-bearing header) must not leak across origins. Mirrors the
// host-pin tests for the auth RTs, applied to the headers
// RoundTripper.
func TestWithHostPinnedHeaders_DropsOnMismatch(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	rt := WithHostPinnedHeaders(http.DefaultTransport, map[string]string{
		"X-Scope-OrgID":   "tenant-a",
		"X-Gateway-Token": "g1",
	}, "configured.example:9093")
	roundTripOnce(t, rt, srv)

	require.Empty(t, srvCap.headers.Get("X-Scope-Orgid"),
		"tenant header must NOT be injected on a request to a non-matching host")
	require.Empty(t, srvCap.headers.Get("X-Gateway-Token"),
		"arbitrary auth-bearing headers must NOT be injected on a non-matching host either")
}

// TestWithHostPinnedHeaders_AppliesOnMatch is the positive case:
// configured backend host = request host = headers reach the wire.
func TestWithHostPinnedHeaders_AppliesOnMatch(t *testing.T) {
	t.Parallel()

	srvCap := &captureHandler{}
	srv := httptest.NewServer(srvCap)
	t.Cleanup(srv.Close)

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	rt := WithHostPinnedHeaders(http.DefaultTransport, map[string]string{
		"X-Scope-OrgID": "tenant-a",
	}, srvURL.Host)
	roundTripOnce(t, rt, srv)

	require.Equal(t, "tenant-a", srvCap.headers.Get("X-Scope-Orgid"),
		"matching ExpectedHost must inject the tenant header unchanged")
}

// TestRedirectChain_BasicAuthRTDoesNotReplayCredentials is the
// end-to-end host-pin regression: a 302 from the configured
// backend to a second httptest server must not see the
// Authorization header on the redirect target.
func TestRedirectChain_BasicAuthRTDoesNotReplayCredentials(t *testing.T) {
	t.Parallel()

	// attacker is the redirect target: it captures every header
	// received so the test can assert what arrived.
	attacker := &captureHandler{}
	attackerSrv := httptest.NewServer(attacker)
	t.Cleanup(attackerSrv.Close)

	// configured is the backend the operator pointed a10r at: it
	// responds 302 to the attacker URL on every request.
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attackerSrv.URL, http.StatusFound)
	}))
	t.Cleanup(configured.Close)

	configuredURL, err := url.Parse(configured.URL)
	require.NoError(t, err)

	authedRT, err := NewAuth(AuthOptions{
		BasicAuth:    &config.BasicAuth{Username: "alice", Password: "s3cret"},
		ExpectedHost: configuredURL.Host,
	}, http.DefaultTransport)
	require.NoError(t, err)

	// Drive the redirect through a stock http.Client so the
	// stdlib's redirect-following is the only thing under test
	// here. The vanilla.Client variant adds CheckRedirect on top
	// — exercised separately in vanilla/client_test.go.
	client := &http.Client{Transport: authedRT}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, configured.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Empty(t, attacker.headers.Get(headerAuthorization),
		"basic auth must NOT be replayed on the cross-origin redirect target")
}

// TestBuildTLSConfig_WarnsOnInsecureSkipVerify pins the MITM-surface
// warning. The nolint:gosec on the field assignment is not a
// substitute for an operator-visible signal: programmatic callers
// (tests, future REPLs) that bypass config validation must still
// see a WARN log. Sequential — slog.SetDefault is process-wide.
func TestBuildTLSConfig_WarnsOnInsecureSkipVerify(t *testing.T) {
	buf := captureSlogDefault(t)

	cfg, err := buildTLSConfig(&config.TLSConfig{InsecureSkipVerify: true})
	require.NoError(t, err)
	require.True(t, cfg.InsecureSkipVerify)

	require.Contains(t, buf.String(), "TLS certificate verification disabled",
		"InsecureSkipVerify=true must surface a WARN — MITM possible")
	require.Contains(t, buf.String(), "level=WARN")
}

// TestBuildTLSConfig_WarnsOnCustomCAInline pins the trust-narrowing
// warning for inline CA bundles. runTUI logs an INFO at startup but
// any programmatic caller wiring NewBase directly never sees it; the
// warning must come from buildTLSConfig itself.
func TestBuildTLSConfig_WarnsOnCustomCAInline(t *testing.T) {
	buf := captureSlogDefault(t)

	pem := generateSelfSignedCA(t)
	cfg, err := buildTLSConfig(&config.TLSConfig{CA: pem})
	require.NoError(t, err)
	require.NotNil(t, cfg.RootCAs)

	require.Contains(t, buf.String(), "custom CA bundle replaces system roots",
		"inline CA must surface a WARN — trust narrows to the configured bundle")
	require.Contains(t, buf.String(), "level=WARN")
	require.Contains(t, buf.String(), "ca_source=inline",
		"warning must carry the ca_source attr so operators can locate the override")
}

// TestBuildTLSConfig_NoWarningsOnSafeSpec is the baseline: an empty
// TLSConfig (no InsecureSkipVerify, no CA) must NOT emit either
// warning. Guards against false positives that would dull the signal.
func TestBuildTLSConfig_NoWarningsOnSafeSpec(t *testing.T) {
	buf := captureSlogDefault(t)

	_, err := buildTLSConfig(&config.TLSConfig{ServerName: "am.internal", MinVersion: "TLS12"})
	require.NoError(t, err)

	require.NotContains(t, buf.String(), "TLS certificate verification disabled",
		"safe TLS spec must not trigger the InsecureSkipVerify warning")
	require.NotContains(t, buf.String(), "custom CA bundle replaces system roots",
		"safe TLS spec must not trigger the custom-CA warning")
}

// SPDX-License-Identifier: Apache-2.0

package factory

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// observingHandler captures one request's path + a tenant header +
// the User-Agent so the factory tests can assert that vanilla /
// Mimir scenarios produce wire-correct requests.
type observingHandler struct {
	calls atomic.Int32
	path  atomic.Pointer[string]
	tHead atomic.Pointer[string]
	ua    atomic.Pointer[string]
}

func (h *observingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	p := r.URL.Path
	h.path.Store(&p)
	// http.Header.Get canonicalises lookups, so "X-Scope-Orgid" reads
	// the same value clients send as "X-Scope-OrgID" (Mimir's
	// documented casing). The canonical form here keeps the linter
	// happy without changing behaviour.
	t := r.Header.Get("X-Scope-Orgid")
	h.tHead.Store(&t)
	ua := r.Header.Get("User-Agent")
	h.ua.Store(&ua)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("[]"))
}

func TestBuild_VanillaScenario(t *testing.T) {
	t.Parallel()

	// Vanilla = empty prefix, no tenant header. ADR 0028's
	// "Vanilla AM is just prefix='' tenant_header=''" is the contract.
	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{Name: "prod", URL: srv.URL}, "")
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "/api/v2/alerts", *h.path.Load(),
		"vanilla path must NOT carry a prefix")
	require.Empty(t, *h.tHead.Load(),
		"vanilla must NOT inject a tenant header")
}

func TestBuild_MimirScenario(t *testing.T) {
	t.Parallel()

	// Mimir multi-tenant: prefix + tenant header per ADR 0028.
	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{
		Name:         "staging",
		URL:          srv.URL,
		Prefix:       "/alertmanager",
		TenantHeader: "X-Scope-OrgID",
		Tenant:       "tenant-a",
	}, "")
	require.NoError(t, err, "tenant_header/tenant sugar must round-trip through the factory")

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "/alertmanager/api/v2/alerts", *h.path.Load())
	require.Equal(t, "tenant-a", *h.tHead.Load())
}

func TestBuild_RejectsEmptyURL(t *testing.T) {
	t.Parallel()

	_, err := Build(config.Backend{Name: "broken"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "broken",
		"error must name the backend so multi-backend setups know which entry failed")
}

func TestBuild_PropagatesAuthError(t *testing.T) {
	t.Parallel()

	_, err := Build(config.Backend{
		Name:      "bad-auth",
		URL:       "http://x",
		BasicAuth: &config.BasicAuth{Username: "u"}, // no password
	}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bad-auth",
		"transport-layer error must surface with the backend name")
}

func TestBuild_PropagatesCapabilities(t *testing.T) {
	t.Parallel()

	c, err := Build(config.Backend{
		Name: "mimir-admin",
		URL:  "http://x",
		Capabilities: config.Capabilities{
			ConfigAPI:   true,
			TenantAdmin: true,
			Ring:        false,
		},
	}, "")
	require.NoError(t, err)

	caps := c.Capabilities()
	require.True(t, caps.ConfigAPI)
	require.True(t, caps.TenantAdmin)
	require.False(t, caps.Ring)
}

func TestBuild_InjectsUserAgent(t *testing.T) {
	t.Parallel()

	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{Name: "prod", URL: srv.URL}, "a10r/1.2.3 (abc)")
	require.NoError(t, err)
	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "a10r/1.2.3 (abc)", *h.ua.Load(),
		"factory must wire the User-Agent through the transport stack")
}

func TestBuild_EmptyUserAgentLeavesGoDefault(t *testing.T) {
	t.Parallel()

	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{Name: "prod", URL: srv.URL}, "")
	require.NoError(t, err)
	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	// Empty UA short-circuits the wrapper; Go's http stack supplies its
	// own default User-Agent ("Go-http-client/...") so the header is
	// non-empty. The contract is "we don't override", not "header is
	// missing entirely".
	require.NotEmpty(t, *h.ua.Load(),
		"empty UA must leave Go's default User-Agent in place")
	require.NotContains(t, *h.ua.Load(), "a10r",
		"empty UA must not synthesise an a10r-prefixed string")
}

// TestBuild_PrometheusShapedConfigPastesCleanly is the regression that
// guards the design goal: a Prometheus-shaped backend (basic_auth +
// custom headers + remote_timeout) flows through Build without
// translation and the wire requests carry the expected headers.
func TestBuild_PrometheusShapedConfigPastesCleanly(t *testing.T) {
	t.Parallel()

	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	be := config.Backend{
		Name:      "prom-shape",
		URL:       srv.URL,
		BasicAuth: &config.BasicAuth{Username: "alice", Password: "s3cret"},
		Headers: map[string]string{
			"X-Scope-OrgID": "tenant-a",
			"X-Trace-Id":    "factory-test",
		},
		RemoteTimeout: time.Second,
	}
	require.NoError(t, be.Validate(), "the fixture must satisfy the schema validator")

	c, err := Build(be, "")
	require.NoError(t, err)
	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "tenant-a", *h.tHead.Load(),
		"tenant header must reach the wire when supplied via headers map")
}

// TestBuild_WithDebugLogEmitsRequestLog proves the WithDebugLog
// option threads through to the constructed client's RoundTripper:
// a real request emits a structured log line at LevelDebug. The
// redaction half is covered in transport package tests; this one
// only confirms the wiring is alive.
func TestBuild_WithDebugLogEmitsRequestLog(t *testing.T) {
	t.Parallel()

	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := Build(
		config.Backend{Name: "prod", URL: srv.URL},
		"a10r/test",
		WithDebugLog(logger),
	)
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "level=DEBUG", "WithDebugLog must wire the debug RoundTripper")
	require.Contains(t, out, "msg=http")
	require.Contains(t, out, "method=GET")
	require.Contains(t, out, "/api/v2/alerts")
}

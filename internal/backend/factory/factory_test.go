// SPDX-License-Identifier: Apache-2.0

package factory

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// observingHandler captures one request's path + a tenant header so
// the factory tests can assert that vanilla / Mimir scenarios
// produce wire-correct requests.
type observingHandler struct {
	calls atomic.Int32
	path  atomic.Pointer[string]
	tHead atomic.Pointer[string]
}

func (h *observingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	p := r.URL.Path
	h.path.Store(&p)
	t := r.Header.Get("X-Scope-OrgID")
	h.tHead.Store(&t)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte("[]"))
}

func TestBuild_VanillaScenario(t *testing.T) {
	t.Parallel()

	// Vanilla = empty prefix, no tenant header. Audit §5.1's
	// "Vanilla AM is just prefix='' tenant_header=''" is the contract.
	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{Name: "prod", URL: srv.URL})
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

	// Mimir multi-tenant: prefix + tenant header per audit §2.2.
	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{
		Name:         "staging",
		URL:          srv.URL,
		Prefix:       "/alertmanager",
		TenantHeader: "X-Scope-OrgID",
		Tenant:       "tenant-a",
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "/alertmanager/api/v2/alerts", *h.path.Load())
	require.Equal(t, "tenant-a", *h.tHead.Load())
}

func TestBuild_MimirSingleTenantNoHeader(t *testing.T) {
	t.Parallel()

	// Mimir running with `-auth.multitenancy-enabled=false` per audit
	// §2.2: prefix is set but no tenant header is sent.
	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := Build(config.Backend{
		Name:   "single",
		URL:    srv.URL,
		Prefix: "/alertmanager",
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "/alertmanager/api/v2/alerts", *h.path.Load())
	require.Empty(t, *h.tHead.Load(),
		"empty TenantHeader must not inject a tenant header")
}

func TestBuild_RejectsEmptyURL(t *testing.T) {
	t.Parallel()

	_, err := Build(config.Backend{Name: "broken"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "broken",
		"error must name the backend so multi-backend setups know which entry failed")
}

func TestBuild_PropagatesAuthError(t *testing.T) {
	t.Parallel()

	_, err := Build(config.Backend{
		Name: "bad-auth",
		URL:  "http://x",
		Auth: &config.AuthSpec{
			Type:  config.AuthTypeBasic,
			Basic: &config.BasicAuth{Username: "u"}, // no password
		},
	})
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
	})
	require.NoError(t, err)

	caps := c.Capabilities()
	require.True(t, caps.ConfigAPI)
	require.True(t, caps.TenantAdmin)
	require.False(t, caps.Ring)
}

func TestBuild_ImplementsClient(t *testing.T) {
	t.Parallel()

	// Compile-time assertion: every Build() return value satisfies
	// the full backend.Client. Catches a future divergence between
	// the factory return type and the interface.
	var c backend.Client
	var err error
	c, err = Build(config.Backend{Name: "x", URL: "http://x"})
	require.NoError(t, err)
	require.NotNil(t, c)
}

// SPDX-License-Identifier: Apache-2.0

package mimir

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// observingHandler records the URL path + a few headers of every
// request so tests can assert prefix-and-header round-trip.
type observingHandler struct {
	calls atomic.Int32
	path  atomic.Pointer[string]
	tHead atomic.Pointer[string]
	auth  atomic.Pointer[string]
	body  []byte
}

func (h *observingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	p := r.URL.Path
	h.path.Store(&p)
	t := r.Header.Get("X-Scope-Orgid")
	h.tHead.Store(&t)
	a := r.Header.Get("Authorization")
	h.auth.Store(&a)
	w.Header().Set("Content-Type", "application/json")
	if h.body != nil {
		_, _ = w.Write(h.body)
		return
	}
	_, _ = w.Write([]byte("[]"))
}

func TestNew_AppliesPrefixAndTenantHeader(t *testing.T) {
	t.Parallel()

	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New(ClientConfig{
		BaseURL:      srv.URL,
		Prefix:       "/alertmanager",
		TenantHeader: "X-Scope-OrgID",
		Tenant:       "tenant-a",
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.EqualValues(t, 1, h.calls.Load())
	require.Equal(t, "/alertmanager/api/v2/alerts", *h.path.Load(),
		"prefix must precede /api/v2/...")
	require.Equal(t, "tenant-a", *h.tHead.Load(),
		"tenant header must reach the wire")
}

func TestNew_NoTenantHeaderDoesNotInject(t *testing.T) {
	t.Parallel()

	// Single-tenant Mimir (auth.multitenancy-enabled=false): empty
	// TenantHeader means the request goes through without injection.
	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New(ClientConfig{
		BaseURL: srv.URL,
		Prefix:  "/alertmanager",
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)
	require.Empty(t, *h.tHead.Load(),
		"empty TenantHeader must not inject anything")
}

func TestNew_AuthAndTenantCompose(t *testing.T) {
	t.Parallel()

	h := &observingHandler{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New(ClientConfig{
		BaseURL:      srv.URL,
		Prefix:       "/alertmanager",
		TenantHeader: "X-Scope-OrgID",
		Tenant:       "tenant-b",
		Auth: &config.AuthSpec{
			Type:   config.AuthTypeBearer,
			Bearer: &config.BearerAuth{Token: "tok"},
		},
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)

	require.Equal(t, "tenant-b", *h.tHead.Load())
	require.Equal(t, "Bearer tok", *h.auth.Load(),
		"auth and tenant header must both reach the wire")
}

func TestNew_MalformedAuthSurfaces(t *testing.T) {
	t.Parallel()

	_, err := New(ClientConfig{
		BaseURL: "http://x",
		Auth: &config.AuthSpec{
			Type:  config.AuthTypeBasic,
			Basic: &config.BasicAuth{Username: "u"}, // missing password
		},
	})
	require.Error(t, err, "malformed auth must surface at construction time")
}

func TestNew_CapabilityStubsStillReturnUnsupported(t *testing.T) {
	t.Parallel()

	// v0.1: even with all caps on, the underlying vanilla.Client's
	// stubs return ErrUnsupported. The post-v0.1 config editor will
	// override these on the Mimir side.
	c, err := New(ClientConfig{
		BaseURL: "http://x",
		Caps:    backend.Caps{ConfigAPI: true, TenantAdmin: true, Ring: true},
	})
	require.NoError(t, err)

	_, err = c.GetConfig(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.ListTenantConfigs(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.RingStatus(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)
}

func TestNew_CapsPropagatedToVanilla(t *testing.T) {
	t.Parallel()

	// Caps must propagate to the underlying vanilla client so the
	// TUI can consult Capabilities() to decide what menu actions to
	// offer — regardless of whether the methods are implemented.
	c, err := New(ClientConfig{
		BaseURL: "http://x",
		Caps:    backend.Caps{ConfigAPI: true, TenantAdmin: true, Ring: false},
	})
	require.NoError(t, err)

	caps := c.Capabilities()
	require.True(t, caps.ConfigAPI)
	require.True(t, caps.TenantAdmin)
	require.False(t, caps.Ring)
}

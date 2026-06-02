// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// silenceServer simulates the AM v2 silences endpoints with enough
// fidelity to exercise create/update/expire round-trips. It is not
// a full Alertmanager — the goal is to pin our wire shape, not to
// re-implement AM.
type silenceServer struct {
	mu     sync.Mutex
	stored map[string]wirePostableSilence
	nextID int
}

func newSilenceServer() *silenceServer {
	return &silenceServer{stored: map[string]wirePostableSilence{}}
}

func (s *silenceServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/silences":
		var posted wirePostableSilence
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if posted.ID == "" {
			s.nextID++
			posted.ID = "sil-test-" + strconv.Itoa(s.nextID)
		}
		s.stored[posted.ID] = posted
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(wirePostSilenceResponse{SilenceID: posted.ID}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v2/silence/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/silence/")
		if _, ok := s.stored[id]; !ok {
			http.Error(w, "no such silence", http.StatusNotFound)
			return
		}
		delete(s.stored, id)
		w.WriteHeader(http.StatusOK)

	default:
		http.NotFound(w, r)
	}
}

func TestCreateSilence_RoundTrip(t *testing.T) {
	t.Parallel()

	server := newSilenceServer()
	srv := httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	id, err := c.CreateSilence(t.Context(), backend.SilenceSpec{
		Matchers: []backend.Matcher{
			{Name: "alertname", Value: "DiskFull", IsEqual: true},
		},
		StartsAt:  time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
		EndsAt:    time.Date(2026, 4, 25, 11, 0, 0, 0, time.UTC),
		CreatedBy: "alice",
		Comment:   "test",
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Contains(t, server.stored, id)
	require.Equal(t, "alice", server.stored[id].CreatedBy)
	require.Len(t, server.stored[id].Matchers, 1)
	require.NotNil(t, server.stored[id].Matchers[0].IsEqual,
		"isEqual must be emitted explicitly so the server sees user intent")
	require.True(t, *server.stored[id].Matchers[0].IsEqual)
}

func TestUpdateSilence_PreservesID(t *testing.T) {
	t.Parallel()

	server := newSilenceServer()
	server.stored["sil-existing"] = wirePostableSilence{ID: "sil-existing", CreatedBy: "alice"}
	srv := httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	err := c.UpdateSilence(t.Context(), "sil-existing", backend.SilenceSpec{
		CreatedBy: "alice",
		Comment:   "updated",
	})
	require.NoError(t, err)

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, "updated", server.stored["sil-existing"].Comment,
		"update must preserve the id and overwrite the rest")
}

func TestExpireSilence_RoundTrip(t *testing.T) {
	t.Parallel()

	server := newSilenceServer()
	server.stored["sil-existing"] = wirePostableSilence{ID: "sil-existing"}
	srv := httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	require.NoError(t, c.ExpireSilence(t.Context(), "sil-existing"))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.NotContains(t, server.stored, "sil-existing")
}

func TestExpireSilence_RequiresID(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	err := c.ExpireSilence(t.Context(), "")
	require.Error(t, err)
}

func TestExpireSilence_NotFound(t *testing.T) {
	t.Parallel()

	server := newSilenceServer()
	srv := httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	err := c.ExpireSilence(t.Context(), "no-such-id")
	require.Error(t, err)
	require.Contains(t, err.Error(), "404", "404 status must be visible in the error")
}

func TestCapabilityStubs_AllReturnUnsupported(t *testing.T) {
	t.Parallel()

	c, err := New(ClientConfig{BaseURL: "http://x"})
	require.NoError(t, err)

	_, err = c.GetConfig(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)

	require.ErrorIs(t, c.SetConfig(t.Context(), backend.MimirConfig{}), backend.ErrUnsupported)
	require.ErrorIs(t, c.DeleteConfig(t.Context()), backend.ErrUnsupported)

	_, err = c.ListTenantConfigs(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)

	_, err = c.RingStatus(t.Context())
	require.ErrorIs(t, err, backend.ErrUnsupported)
}

// SPDX-License-Identifier: Apache-2.0

package vanilla

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// fixtureHandler serves the named testdata/<file>.json fixture for
// the given path prefix. Anything else 404s — verifies tests hit
// the URL they claim.
func fixtureHandler(t *testing.T, routes map[string]string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	for pattern, fixture := range routes {
		path := filepath.Join("testdata", fixture)
		body, err := os.ReadFile(path)
		require.NoError(t, err)

		mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		})
	}
	return mux
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(ClientConfig{BaseURL: srv.URL, Timeout: 5 * time.Second})
	require.NoError(t, err)
	return c
}

func TestNew_ValidatesBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "empty", baseURL: "", wantErr: true},
		{name: "valid", baseURL: "http://localhost:9093"},
		{name: "with prefix", baseURL: "https://am.internal"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(ClientConfig{BaseURL: tc.baseURL})
			if tc.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidBaseURL)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClient_ListAlerts(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(fixtureHandler(t, map[string]string{
		"/api/v2/alerts": "list_alerts.json",
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	got, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, "abc123", got[0].Fingerprint)
	require.Equal(t, "HighCPU", got[0].Labels["alertname"])
	require.Equal(t, backend.AlertStateActive, got[0].State)
	require.Equal(t, []string{"team-ops", "pager"}, got[0].Receivers)
	require.Empty(t, got[0].SilencedBy)
	require.Empty(t, got[0].InhibitedBy)
	require.Empty(t, got[0].MutedBy)

	// Second alert exercises UTF-8 labels, missing endsAt (zero time)
	// and the three suppression-reason buckets — silencedBy /
	// inhibitedBy / mutedBy — populated on a `suppressed` row.
	require.Equal(t, "eu-west", got[1].Labels["régión"], "UTF-8 label values must round-trip")
	require.True(t, got[1].EndsAt.IsZero(), "missing endsAt must decode as zero time")
	require.Equal(t, []string{"sil-789"}, got[1].SilencedBy)
	require.Equal(t, []string{"0006251c575c1dd0"}, got[1].InhibitedBy,
		"inhibitedBy must round-trip through wire → domain")
	require.Equal(t, []string{"out-of-hours", "weekends"}, got[1].MutedBy,
		"mutedBy must round-trip through wire → domain")
}

func TestClient_ListAlerts_FilterParamsReachServer(t *testing.T) {
	t.Parallel()

	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	active := true
	silenced := false
	_, err := c.ListAlerts(t.Context(), backend.AlertFilter{
		Active:   &active,
		Silenced: &silenced,
		Filter:   []string{`alertname="High CPU"`, `severity=~".*"`},
		Receiver: "team-ops",
	})
	require.NoError(t, err)
	require.Equal(t, "true", capturedQuery.Get("active"))
	require.Equal(t, "false", capturedQuery.Get("silenced"))
	require.Equal(t, []string{`alertname="High CPU"`, `severity=~".*"`}, capturedQuery["filter"])
	require.Equal(t, "team-ops", capturedQuery.Get("receiver"))
}

func TestClient_ListAlertGroups(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(fixtureHandler(t, map[string]string{
		"/api/v2/alerts/groups": "list_alert_groups.json",
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	got, err := c.ListAlertGroups(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "ops", got[0].Labels["team"])
	require.Len(t, got[0].Alerts, 2)
}

func TestClient_ListSilences(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(fixtureHandler(t, map[string]string{
		"/api/v2/silences": "list_silences.json",
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	got, err := c.ListSilences(t.Context(), backend.SilenceFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, "sil-789", got[0].ID)
	require.Equal(t, backend.SilenceStateActive, got[0].State)
	require.Len(t, got[0].Matchers, 2)
	require.True(t, got[0].Matchers[0].IsEqual,
		"isEqual omitted in JSON must default to true (positive matcher)")
	require.True(t, got[0].Matchers[1].IsRegex)

	require.Equal(t, backend.SilenceStateExpired, got[1].State)
}

func TestClient_GetSilence_RequiresID(t *testing.T) {
	t.Parallel()

	c := newTestClient(t, httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	_, err := c.GetSilence(t.Context(), "")
	require.Error(t, err)
}

func TestClient_ListReceivers(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(fixtureHandler(t, map[string]string{
		"/api/v2/receivers": "list_receivers.json",
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	got, err := c.ListReceivers(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "team-ops", got[0].Name)
}

func TestClient_Status(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(fixtureHandler(t, map[string]string{
		"/api/v2/status": "status.json",
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	got, err := c.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, "ready", got.Cluster.Status)
	require.Len(t, got.Cluster.Peers, 2)
	require.Equal(t, "0.28.1", got.Version.Version)
	require.Contains(t, got.Config, "resolve_timeout")
	require.Greater(t, got.Uptime, time.Duration(0), "uptime must be a positive duration")
}

func TestClient_EmptyListDecodesAsEmptySlice(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	got, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestClient_PrefixIsHonored(t *testing.T) {
	t.Parallel()

	// Mimir-style: BaseURL + Prefix + /api/v2/...
	srv := httptest.NewServer(fixtureHandler(t, map[string]string{
		"/alertmanager/api/v2/alerts": "list_alerts.json",
	}))
	t.Cleanup(srv.Close)

	c, err := New(ClientConfig{BaseURL: srv.URL, Prefix: "/alertmanager"})
	require.NoError(t, err)
	got, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestClient_401IsUnauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err)
	require.ErrorIs(t, err, backend.ErrUnauthorized)
	require.False(t, backend.Retryable(err), "401 must not enter the C1 backoff loop")
}

func TestClient_403IsUnauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err)
	require.ErrorIs(t, err, backend.ErrUnauthorized)
}

func TestClient_5xxIsRetryable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err)
	require.True(t, backend.Retryable(err), "5xx must opt into the C1 backoff loop")
}

func TestClient_429IsRetryable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err)
	require.True(t, backend.Retryable(err), "429 must opt into the C1 backoff loop")
}

func TestClient_RefusedConnectionIsUnreachable(t *testing.T) {
	t.Parallel()

	// Pin a TCP port that nothing is listening on. ":1" is reserved
	// and refused on every OS we target.
	c, err := New(ClientConfig{BaseURL: "http://127.0.0.1:1", Timeout: time.Second})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err)
	require.ErrorIs(t, err, backend.ErrUnreachable)
	require.True(t, backend.Retryable(err), "ErrUnreachable must opt into the C1 backoff loop")
}

func TestClient_ContextCancelStopsRequest(t *testing.T) {
	t.Parallel()

	// Server hangs forever; cancelling the context must abort the
	// in-flight request quickly so the polling loop doesn't stall.
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(hold)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := c.ListAlerts(ctx, backend.AlertFilter{})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, backend.ErrUnreachable),
		"cancelled request must surface as ctx.Canceled or ErrUnreachable, got %v", err)
}

// TestClient_LimitsResponseBodySize is the audit F14 regression:
// the JSON decoder must never read more than maxResponseBodyBytes
// from the response body so a hostile backend cannot OOM the TUI
// by streaming a multi-gigabyte payload. The test temporarily
// shrinks the cap so the assertion runs in O(KB) rather than O(MB).
func TestClient_LimitsResponseBodySize(t *testing.T) {
	// Mutating the package-level cap means we can't run in
	// parallel with the other vanilla tests, so this one is
	// intentionally sequential.

	original := maxResponseBodyBytes
	maxResponseBodyBytes = 64 // 64 bytes — well below any real payload
	t.Cleanup(func() { maxResponseBodyBytes = original })

	// Server emits a JSON array large enough that decoding would
	// require reading past the cap. The decoder hits EOF (from
	// LimitReader) before the closing bracket and surfaces a
	// decode error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 1 KiB body — much larger than the 64-byte test cap.
		_, _ = w.Write([]byte("[" + repeat(`{"fingerprint":"abcdef0123456789","labels":{}},`, 32) + "{}]"))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, err := c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err, "decode must fail when the response exceeds maxResponseBodyBytes")
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

// TestClient_RefusesCrossOriginRedirect is the audit's
// belt-and-braces regression for F1: a 302 to a different origin
// must produce an ErrCrossOriginRedirect rather than a successful
// request to the attacker target. Same shape as the transport-level
// host-pin tests but exercises the http.Client.CheckRedirect path
// installed by ClientConfig.ExpectedHost.
func TestClient_RefusesCrossOriginRedirect(t *testing.T) {
	t.Parallel()

	// attacker is the redirect target — its handler should never
	// fire; the test asserts the redirect was refused before it
	// could be followed.
	attackerCalled := false
	attackerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(attackerSrv.Close)

	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attackerSrv.URL, http.StatusFound)
	}))
	t.Cleanup(configured.Close)

	configuredURL, err := url.Parse(configured.URL)
	require.NoError(t, err)

	c, err := New(ClientConfig{
		BaseURL:      configured.URL,
		ExpectedHost: configuredURL.Host,
		Timeout:      2 * time.Second,
	})
	require.NoError(t, err)

	_, err = c.ListAlerts(t.Context(), backend.AlertFilter{})
	require.Error(t, err)
	require.False(t, attackerCalled,
		"attacker handler must NOT be reached — CheckRedirect must abort the redirect chain")
	require.ErrorIs(t, err, ErrCrossOriginRedirect,
		"cross-origin refusal must surface as ErrCrossOriginRedirect")
}

// TestClient_AllowsSameHostRedirect pins the positive case so the
// CheckRedirect callback doesn't accidentally block legitimate
// same-origin redirects (e.g. /api -> /api/v2 path normalisation).
func TestClient_AllowsSameHostRedirect(t *testing.T) {
	t.Parallel()

	// Same-host redirect: /redirect -> /api/v2/alerts on the same
	// server. Should follow and produce a normal response.
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/v2/alerts", http.StatusFound)
	})
	mux.Handle("/api/v2/alerts", fixtureHandler(t, map[string]string{
		"/api/v2/alerts": "list_alerts.json",
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	c, err := New(ClientConfig{
		BaseURL:      srv.URL,
		ExpectedHost: srvURL.Host,
		Timeout:      2 * time.Second,
	})
	require.NoError(t, err)

	// Drill the redirect by issuing a raw GET against /redirect.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/redirect", http.NoBody)
	require.NoError(t, err)
	resp, err := c.http.Do(req)
	require.NoError(t, err, "same-host redirect must be allowed by CheckRedirect")
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_Capabilities_PassThrough(t *testing.T) {
	t.Parallel()

	c, err := New(ClientConfig{
		BaseURL: "http://x",
		Caps:    backend.Caps{ConfigAPI: true, TenantAdmin: true},
	})
	require.NoError(t, err)
	require.True(t, c.Capabilities().ConfigAPI)
	require.True(t, c.Capabilities().TenantAdmin)
	require.False(t, c.Capabilities().Ring)
}

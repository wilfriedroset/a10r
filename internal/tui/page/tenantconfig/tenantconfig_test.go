// SPDX-License-Identifier: Apache-2.0

package tenantconfig

import (
	"context"
	"errors"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// fakeFetcher captures the call and replies with a fixed
// Status. Tests that need a delay can set ch and signal manually.
type fakeFetcher struct {
	cfg string
	err error
}

func (f *fakeFetcher) Status(_ context.Context) (backend.Status, error) {
	if f.err != nil {
		return backend.Status{}, f.err
	}
	return backend.Status{Config: f.cfg}, nil
}

// blockingFetcher.Status signals `started` then waits for ctx to
// cancel. Lets tests observe whether the page cancels its in-flight
// fetch on Close.
type blockingFetcher struct {
	started chan struct{}
}

func (f *blockingFetcher) Status(ctx context.Context) (backend.Status, error) {
	close(f.started)
	<-ctx.Done()
	return backend.Status{}, ctx.Err()
}

func TestRedactedBackendYAML_RedactsBasicPassword(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:      "prod",
		URL:       "http://am",
		BasicAuth: &config.BasicAuth{Username: "alice", Password: "hunter2"},
	})
	require.NoError(t, err)
	// Round-trip the YAML so the assertion is semantic — yaml.v3's
	// quoting choice for a `***` scalar is implementation-defined
	// (`***` happens to be single-quoted today, but a future
	// version or different marker like `<redacted>` would render
	// without quotes). What matters is the *parsed* value, plus
	// the absence of the secret literal in the rendered text.
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	// Audit F12 partial-redact rule: keep first 2 chars, mask the
	// remainder with the redaction marker so the operator can
	// disambiguate similar configs without fully exposing the
	// account name to over-the-shoulder observers.
	require.Equal(t, "al"+redactionMarker, got.BasicAuth.Username)
	require.Equal(t, redactionMarker, got.BasicAuth.Password)
	require.NotContains(t, body, "hunter2",
		"the password must never reach the rendered output")
	require.NotContains(t, body, "alice",
		"the full username must never reach the rendered output once partial-redact is in effect")
}

// TestRedactedBackendYAML_PartialRedactsShortUsernameFully pins
// the F12 length-floor rule: usernames shorter than 4 characters
// fully redact to the marker so a 2-char account name never
// reveals every character.
func TestRedactedBackendYAML_PartialRedactsShortUsernameFully(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:      "prod",
		URL:       "http://am",
		BasicAuth: &config.BasicAuth{Username: "wr", Password: "x"},
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, redactionMarker, got.BasicAuth.Username,
		"usernames < 4 chars fully redact to avoid leaking 100%% of the field")
}

func TestRedactedBackendYAML_RedactsBearerToken(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:        "prod",
		URL:         "http://am",
		BearerToken: "eyJabc.def.ghi",
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, redactionMarker, got.BearerToken)
	require.NotContains(t, body, "eyJabc")
}

func TestRedactedBackendYAML_RedactsAuthorizationCredentials(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name: "prod",
		URL:  "http://am",
		Authorization: &config.Authorization{
			Type:        "Bearer",
			Credentials: "eyJabc.def.ghi",
		},
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, "Bearer", got.Authorization.Type,
		"non-secret type stays visible so the user can verify wiring")
	require.Equal(t, redactionMarker, got.Authorization.Credentials)
	require.NotContains(t, body, "eyJabc")
}

func TestRedactedBackendYAML_RedactsHeadersMap(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name: "prod",
		URL:  "http://am",
		Headers: map[string]string{
			"X-API-Key":     "very-secret-key",
			"X-Scope-OrgID": "tenant-1",
		},
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, redactionMarker, got.Headers["X-API-Key"])
	require.Equal(t, redactionMarker, got.Headers["X-Scope-OrgID"],
		"every header value is redacted — we cannot tell secrets from identifiers at the schema level")
	require.NotContains(t, body, "very-secret-key")
}

// yamlUnmarshal is a tiny helper around gopkg.in/yaml.v3 so the
// redaction tests can read parsed values back from the rendered
// body without each test wiring its own import alias.
func yamlUnmarshal(s string, dst any) error {
	return yaml.Unmarshal([]byte(s), dst)
}

// TestRedactedBackendYAML_StripsURLUserinfo pins that credentials
// embedded in a Backend.URL (https://user:pass@host) are removed
// before the inspector renders. Configs that pasted credentials
// into the URL — a common shortcut for proxy authentication — would
// otherwise leak both user and pass at a glance.
func TestRedactedBackendYAML_StripsURLUserinfo(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name: "prod",
		URL:  "https://alice:hunter2@am.example.com/path",
	})
	require.NoError(t, err)
	require.NotContains(t, body, "hunter2", "password must be stripped from URL")
	require.NotContains(t, body, "alice", "userinfo username must be stripped from URL")
	require.Contains(t, body, "am.example.com/path", "host and path must survive the strip")
}

// TestRedactedBackendYAML_StripsProxyURLUserinfo mirrors the URL
// guard for ProxyURL, which commonly carries proxy auth in the
// userinfo segment.
func TestRedactedBackendYAML_StripsProxyURLUserinfo(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:     "prod",
		URL:      "http://am",
		ProxyURL: "http://proxyuser:proxypass@proxy.internal:3128",
	})
	require.NoError(t, err)
	require.NotContains(t, body, "proxypass", "proxy password must be stripped")
	require.NotContains(t, body, "proxyuser", "proxy userinfo username must be stripped")
	require.Contains(t, body, "proxy.internal:3128")
}

// TestPartialRedact_HandlesMultibyteRunes pins that partialRedact
// slices by RUNE not by byte. A 3-byte rune (e.g. "\u4e2d" in UTF-8)
// followed by ASCII can yield s[:2] that splits inside the rune,
// producing invalid UTF-8 like "\xe4\xb8***". The "éric-team" case
// happens to round to a rune boundary by coincidence (é is exactly
// 2 bytes), so we use a 3-byte rune to catch the boundary slice
// directly.
func TestPartialRedact_HandlesMultibyteRunes(t *testing.T) {
	t.Parallel()
	// "\u4e2d" is U+4E2D (CJK ideograph "zhong") — a 3-byte rune
	// in UTF-8. Escape form so the source file stays pure ASCII
	// (no gosmopolitan trigger) while the runtime string still has
	// the multi-byte boundary needed to exercise the bug.
	got := partialRedact("\u4e2dhellouser")
	require.True(t, utf8.ValidString(got),
		"partialRedact must return valid UTF-8 even when slicing a multi-byte input")
	require.Contains(t, got, redactionMarker, "must still redact the tail")
}

func TestRedactedBackendYAML_RedactionDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	cfg := config.Backend{
		Name:      "prod",
		URL:       "http://am",
		BasicAuth: &config.BasicAuth{Username: "alice", Password: "hunter2"},
	}
	_, err := redactedBackendYAML(cfg)
	require.NoError(t, err)
	require.Equal(t, "hunter2", cfg.BasicAuth.Password,
		"redaction must operate on a copy — original config stays intact")
}

func TestPage_BodyShowsRedactedBackendImmediately(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant: "prod",
		Backend: config.Backend{
			Name:        "prod",
			URL:         "http://am",
			BearerToken: "supersecret",
		},
		Styles: testutil.LoadStyles(t),
	})
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "url: http://am")
	require.Contains(t, out, "***")
	require.NotContains(t, out, "supersecret")
}

func TestPage_FetchSucceedsRendersAMConfig(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Fetcher: &fakeFetcher{cfg: "global:\n  resolve_timeout: 5m\n"},
		Styles:  testutil.LoadStyles(t),
	})
	require.True(t, p.loading)

	// Drive Init's Cmd to deliver the statusFetchedMsg.
	cmd := p.Init()
	require.NotNil(t, cmd)
	_, _ = p.Update(cmd())
	require.False(t, p.loading)
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "global:")
	require.Contains(t, out, "resolve_timeout: 5m")
}

func TestPage_FetchFailureSurfacesError(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Fetcher: &fakeFetcher{err: errors.New("backend unreachable")},
		Styles:  testutil.LoadStyles(t),
	})
	cmd := p.Init()
	_, _ = p.Update(cmd())
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "fetch failed:")
	require.Contains(t, out, "backend unreachable")
}

// TestPage_StatusFetchContextCancelDropsSilently pins the defensive
// contract: when the FetchCtx parent is cancelled (typically during
// SIGTERM-driven shutdown), the resulting context.Canceled error
// must NOT surface as "(fetch failed: context canceled)" on the
// last frame. The render path is unreachable in prod because the
// page-pop already removes the page from top, but the audit asked
// for an explicit short-circuit so the contract is visible in code.
func TestPage_StatusFetchContextCancelDropsSilently(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Fetcher: &fakeFetcher{err: context.Canceled},
		Styles:  testutil.LoadStyles(t),
	})
	cmd := p.Init()
	require.NotNil(t, cmd)
	_, _ = p.Update(cmd())
	require.NoError(t, p.amErr,
		"ctx-cancel is shutdown noise; the page must not record it as a fetch error")
	require.False(t, p.loading,
		"loading must flip false so a stale spinner doesn't paint the final frame")
	out := testutil.StripStyle(p.View(120, 30))
	require.NotContains(t, out, "fetch failed",
		"context.Canceled must drop silently — no misleading error on a torn-down frame")
}

func TestPage_NoFetcherRendersStaticMessage(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Styles:  testutil.LoadStyles(t),
	})
	require.False(t, p.loading)
	require.Nil(t, p.Init())
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "(no client available)")
}

// TestPage_VimMotionsScroll is the wiring smoke for the cursor
// module: pressing `j` in Update must route into cursor.HandleMotion
// and advance p.scroll. The full motion contract (j/k/G/g/Ctrl+D/U/F/B,
// clamps, empty-view) lives in
// internal/tui/page/cursor/motion_test.go:TestHandleMotion; this
// test only proves the page is wired to it.
func TestPage_VimMotionsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: testutil.LoadStyles(t)})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.scroll, "Update must route `j` into cursor.HandleMotion")
}

// TestPage_CloseCancelsInflightFetch pins that closing the page
// cancels its in-flight /status fetch instead of letting the
// goroutine leak for the full fetchTimeout window. Without the
// fix, closing an inspector while the backend is slow strands one
// goroutine per close for up to 30s.
func TestPage_CloseCancelsInflightFetch(t *testing.T) {
	t.Parallel()
	fetcher := &blockingFetcher{started: make(chan struct{})}
	p := New(Options{
		Backend:      config.Backend{Name: "prod", URL: "http://am"},
		Fetcher:      fetcher,
		FetchTimeout: 30 * time.Second, // realistic prod value
	})
	cmd := p.Init()
	require.NotNil(t, cmd)

	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- cmd() }()

	select {
	case <-fetcher.started:
	case <-time.After(time.Second):
		t.Fatal("fetcher.Status was never called within 1s")
	}

	p.Close()

	select {
	case msg := <-doneCh:
		fetched, ok := msg.(statusFetchedMsg)
		require.True(t, ok, "fetch must complete with a statusFetchedMsg, not panic")
		require.Error(t, fetched.err,
			"cancelled fetch must surface ctx error rather than silently returning empty data")
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not cancel the in-flight fetch within 2s — goroutine leak window")
	}
}

// TestPage_StatusFetchInheritsParentCtxCancellation pins the
// documented contract: cancelling the app-level parent ctx handed in
// via Options.FetchCtx aborts the in-flight /api/v2/status fetch.
// Without the plumbing the fetch parents on context.Background() and
// only Close() (the page-pop / quit-cascade path) reaches the worker.
// A future caller bypassing Close — a programmatic test, a REPL, a
// shutdown hook firing on the ctx — would see the goroutine outlive
// the page for the full fetchTimeout window.
func TestPage_StatusFetchInheritsParentCtxCancellation(t *testing.T) {
	t.Parallel()
	fetcher := &blockingFetcher{started: make(chan struct{})}
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	p := New(Options{
		Backend:      config.Backend{Name: "prod", URL: "http://am"},
		Fetcher:      fetcher,
		FetchTimeout: 30 * time.Second,
		FetchCtx:     parent,
	})
	cmd := p.Init()
	require.NotNil(t, cmd)

	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- cmd() }()

	select {
	case <-fetcher.started:
	case <-time.After(time.Second):
		t.Fatal("fetcher.Status was never called within 1s")
	}

	// Cancelling the parent ctx must propagate to the in-flight fetch.
	// Without the plumbing the fetch's ctx is parented on Background
	// and this cancel is invisible to it — the test would time out.
	cancelParent()

	select {
	case msg := <-doneCh:
		fetched, ok := msg.(statusFetchedMsg)
		require.True(t, ok, "fetch must complete with a statusFetchedMsg, got %T", msg)
		require.Error(t, fetched.err,
			"cancelled fetch must surface ctx error rather than silently returning empty data")
	case <-time.After(2 * time.Second):
		t.Fatal("parent ctx cancellation did not reach the in-flight fetch — Options.FetchCtx not plumbed")
	}
}

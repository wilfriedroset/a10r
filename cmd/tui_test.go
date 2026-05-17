// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// TestLogTransportSurprises_TLS10MinVersionEmitsWarn pins audit
// F7's resolution: TLS 1.0/1.1 stay in the schema as a connectivity
// escape hatch for legacy backends, but every selection must emit
// a WARN at startup so the operator sees the deprecation on every
// run.
func TestLogTransportSurprises_TLS10MinVersionEmitsWarn(t *testing.T) {
	t.Parallel()
	buf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTransportSurprises(logger, []config.Backend{
		{Name: "legacy", TLSConfig: &config.TLSConfig{MinVersion: "TLS10"}},
	})

	out := buf.String()
	require.Contains(t, out, "level=WARN", "deprecated TLS version must surface as WARN")
	require.Contains(t, out, "min_version=TLS10")
	require.Contains(t, out, "backend=legacy")
}

// TestLogTransportSurprises_InlineCAEmitsInfo pins audit F6's
// resolution: keep the Prometheus-parity replace semantics but
// log INFO once per backend so the operator sees that the inline
// CA pinning is in effect.
func TestLogTransportSurprises_InlineCAEmitsInfo(t *testing.T) {
	t.Parallel()
	buf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTransportSurprises(logger, []config.Backend{
		{Name: "self-signed", TLSConfig: &config.TLSConfig{CA: "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----"}},
	})

	out := buf.String()
	require.Contains(t, out, "level=INFO", "inline CA pinning must surface as INFO")
	require.Contains(t, out, "system CA roots not used")
	require.Contains(t, out, "backend=self-signed")
}

// TestLogTransportSurprises_ProxyFromEnvironmentLogsResolved
// pins audit F9: a backend that opts into proxy_from_environment
// logs which proxy URL the OS env actually resolves to, so the
// operator can spot a HTTPS_PROXY hijack chain on startup. The
// test sets HTTPS_PROXY in the env and asserts the resolved URL
// reaches the structured log.
func TestLogTransportSurprises_ProxyFromEnvironmentLogsResolved(t *testing.T) {
	// Sequential because env mutation is process-wide.
	t.Setenv("HTTPS_PROXY", "http://proxy.internal:3128")
	t.Setenv("HTTP_PROXY", "http://proxy.internal:3128")

	buf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTransportSurprises(logger, []config.Backend{
		{Name: "via-proxy", URL: "https://am.example", ProxyFromEnvironment: true},
	})

	out := buf.String()
	require.Contains(t, out, "level=INFO")
	require.Contains(t, out, "proxy_from_environment active")
	require.Contains(t, out, "backend=via-proxy")
	require.Contains(t, out, "proxy=http://proxy.internal:3128")
}

// TestLogTransportSurprises_NoTLSStaysSilent guards against
// noise creep: a backend without a TLS block must not emit any
// log line.
func TestLogTransportSurprises_NoTLSStaysSilent(t *testing.T) {
	t.Parallel()
	buf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTransportSurprises(logger, []config.Backend{
		{Name: "plain"},
		{Name: "modern", TLSConfig: &config.TLSConfig{MinVersion: "TLS12"}},
	})

	require.Empty(t, buf.String(), "no surprises in scope must produce no log lines")
}

// TestLevelFor_DefaultIsInfo asserts the CLI flag fold:
// neither --debug nor --quiet → Info.
func TestLevelFor_DefaultIsInfo(t *testing.T) {
	t.Parallel()
	require.Equal(t, slog.LevelInfo, levelFor(false, false))
}

// TestLevelFor_DebugWins asserts that --debug raises level to
// Debug. Audit F4: previously slog.Default() (stderr) was used
// regardless; a plumbed --debug must reach the file logger.
func TestLevelFor_DebugWins(t *testing.T) {
	t.Parallel()
	require.Equal(t, slog.LevelDebug, levelFor(true, false))
}

// TestLevelFor_QuietDropsToWarn asserts that --quiet drops level
// to Warn so info-noise vanishes from operator logs.
func TestLevelFor_QuietDropsToWarn(t *testing.T) {
	t.Parallel()
	require.Equal(t, slog.LevelWarn, levelFor(false, true))
}

// TestLevelFor_DebugAndQuietPrefersDebug pins the both-set
// behaviour: debug wins, mirroring reconcileLogLevelFlags's
// "--debug overrides --quiet" warning. Defensive — root.go's
// reconciler already collapses the case to (debug=true,
// quiet=false), but levelFor must remain coherent if the inputs
// arrive uncollapsed.
func TestLevelFor_DebugAndQuietPrefersDebug(t *testing.T) {
	t.Parallel()
	require.Equal(t, slog.LevelDebug, levelFor(true, true))
}

// TestUserAgent_DevBuild covers the goreleaser-skipped local
// build (`go build` with no -X ldflags). The output should be a
// bare `a10r/dev` — no parenthesised commit suffix when commit is
// the sentinel "none".
func TestUserAgent_DevBuild(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a10r/dev", userAgent("dev", "none"))
}

// TestUserAgent_ReleaseBuild covers the goreleaser path: a
// non-default commit appears as a parenthesised RFC 9110 comment
// suffix so backend operators can grep one access-log line back
// to the exact build.
func TestUserAgent_ReleaseBuild(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a10r/1.2.3 (abc1234)", userAgent("1.2.3", "abc1234"))
}

// TestUserAgent_EmptyCommitTreatedAsNone pins the defensive branch:
// an unset commit (empty string) folds into the same branch as the
// sentinel "none" so neither variant produces a stray "()" suffix.
func TestUserAgent_EmptyCommitTreatedAsNone(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a10r/1.2.3", userAgent("1.2.3", ""))
}

// TestBuildTenantRows_PicksUpVersionsByName covers the join
// between configured backends and the startup-fetched version
// map. Backends absent from the map leave Version empty (the
// renderer surfaces it as "—" — that contract is pinned in the
// tenant page tests).
func TestBuildTenantRows_PicksUpVersionsByName(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: []config.Backend{
			{Name: "prod", URL: "http://am-prod"},
			{Name: "staging", URL: "http://am-staging"},
			{Name: "dev", URL: "http://am-dev"},
		},
	}
	versions := map[string]string{
		"prod": "0.27.0",
		"dev":  "0.26.1",
		// staging deliberately absent
	}
	rows := buildTenantRows(cfg, versions)
	require.Len(t, rows, 3)
	byName := map[string]int{}
	for i, r := range rows {
		byName[r.Name] = i
	}
	require.Equal(t, "0.27.0", rows[byName["prod"]].Version)
	require.Equal(t, "0.26.1", rows[byName["dev"]].Version)
	require.Empty(t, rows[byName["staging"]].Version,
		"backends absent from the version map leave Version empty")
	for _, r := range rows {
		require.NotEmpty(t, r.URL, "URL must propagate from config.Backend")
	}
}

// TestBuildTenantRows_EmptyConfig covers the cold-start no-backend
// case — the wizard's pre-config state — to verify the helper
// doesn't panic on a zero Config.
func TestBuildTenantRows_EmptyConfig(t *testing.T) {
	t.Parallel()
	rows := buildTenantRows(&config.Config{}, map[string]string{})
	require.Empty(t, rows)
}

// fakeStatusBackend is a backend.Client stub that drives the
// fetchTenantVersions tests. Only Status() is exercised here so
// the other surface methods stay no-op.
type fakeStatusBackend struct {
	version string
	err     error
	delay   time.Duration
	calls   atomic.Int32
}

func (f *fakeStatusBackend) Status(ctx context.Context) (backend.Status, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return backend.Status{}, ctx.Err() //nolint:wrapcheck // sentinel propagation
		}
	}
	if f.err != nil {
		return backend.Status{}, f.err
	}
	return backend.Status{Version: backend.VersionInfo{Version: f.version}}, nil
}

// the rest of backend.Client returns zero values; the production
// caller never invokes them on a status-only fetch.
func (*fakeStatusBackend) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	return nil, nil
}

func (*fakeStatusBackend) ListAlertGroups(context.Context, backend.AlertFilter) ([]backend.AlertGroup, error) {
	return nil, nil
}

func (*fakeStatusBackend) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	return nil, nil
}

func (*fakeStatusBackend) GetSilence(context.Context, string) (backend.Silence, error) {
	return backend.Silence{}, nil
}

func (*fakeStatusBackend) ListReceivers(context.Context) ([]backend.Receiver, error) {
	return nil, nil
}

func (*fakeStatusBackend) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	return "", nil
}

func (*fakeStatusBackend) UpdateSilence(context.Context, string, backend.SilenceSpec) error {
	return nil
}
func (*fakeStatusBackend) ExpireSilence(context.Context, string) error { return nil }
func (*fakeStatusBackend) Capabilities() backend.Caps                  { return backend.Caps{} }
func (*fakeStatusBackend) GetConfig(context.Context) (backend.MimirConfig, error) {
	return backend.MimirConfig{}, nil
}

func (*fakeStatusBackend) SetConfig(context.Context, backend.MimirConfig) error {
	return nil
}

func (*fakeStatusBackend) DeleteConfig(context.Context) error { return nil }
func (*fakeStatusBackend) ListTenantConfigs(context.Context) ([]backend.TenantConfig, error) {
	return nil, nil
}

func (*fakeStatusBackend) RingStatus(context.Context) (backend.Ring, error) {
	return backend.Ring{}, nil
}

// TestFetchTenantVersions_AggregatesByBackendName covers the happy
// path: every client returns its version, the aggregated map keys
// match the configured backend names exactly.
func TestFetchTenantVersions_AggregatesByBackendName(t *testing.T) {
	t.Parallel()
	clients := map[string]backend.Client{
		"prod":    &fakeStatusBackend{version: "0.27.0"},
		"staging": &fakeStatusBackend{version: "0.26.1"},
	}
	got := fetchTenantVersions(t.Context(), clients)
	require.Equal(t, "0.27.0", got["prod"])
	require.Equal(t, "0.26.1", got["staging"])
}

// TestFetchTenantVersions_SkipsErroringBackends covers the
// resilience contract: a backend whose Status call fails is left
// out of the result rather than failing the entire startup.
func TestFetchTenantVersions_SkipsErroringBackends(t *testing.T) {
	t.Parallel()
	clients := map[string]backend.Client{
		"prod": &fakeStatusBackend{version: "0.27.0"},
		"down": &fakeStatusBackend{err: errors.New("connection refused")},
	}
	got := fetchTenantVersions(t.Context(), clients)
	require.Equal(t, "0.27.0", got["prod"])
	_, hasDown := got["down"]
	require.False(t, hasDown,
		"erroring backends must drop out of the aggregated map; "+
			"the tenant page renders missing entries as `—`")
}

// TestFetchTenantVersions_RespectsCancelledContext covers the
// "user kills a10r before startup completes" path — every fetch
// goroutine must observe the parent ctx cancellation rather than
// hang past program exit.
func TestFetchTenantVersions_RespectsCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // immediately cancel
	clients := map[string]backend.Client{
		"slow": &fakeStatusBackend{delay: 10 * time.Second, version: "0.27.0"},
	}
	got := fetchTenantVersions(ctx, clients)
	_, hasSlow := got["slow"]
	require.False(t, hasSlow,
		"cancelled ctx must propagate into the per-backend Status call so a "+
			"backend whose RPC was cancelled mid-flight drops out cleanly")
}

// TestFetchTenantVersions_EmptyClientMap is the cold-start branch
// (no backends configured yet, e.g. before the wizard runs).
// Should return an empty map without panicking or spawning
// goroutines.
func TestFetchTenantVersions_EmptyClientMap(t *testing.T) {
	t.Parallel()
	got := fetchTenantVersions(t.Context(), map[string]backend.Client{})
	require.Empty(t, got)
}

// TestTenantConfigIndex_KeyedByBackendName covers the small
// helper that powers the drill factory's name→config lookup.
// Pure but worth pinning to catch a future refactor that
// silently drops a field.
func TestTenantConfigIndex_KeyedByBackendName(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: []config.Backend{
			{Name: "prod", URL: "http://am-prod"},
			{Name: "staging", URL: "http://am-staging"},
		},
	}
	got := tenantConfigIndex(cfg)
	require.Equal(t, "http://am-prod", got["prod"].URL)
	require.Equal(t, "http://am-staging", got["staging"].URL)
	_, hasMissing := got["dev"]
	require.False(t, hasMissing)
}

func TestPageInterval_PageOverrideWins(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Defaults: config.Defaults{PollInterval: 30 * time.Second},
		Pages:    config.PageOverrides{Alerts: config.PageConfig{PollInterval: 5 * time.Second}},
	}
	be := config.Backend{Name: "prod", URL: "http://am", PollInterval: 60 * time.Second}

	require.Equal(t, 5*time.Second, pageInterval(be, cfg, "alerts"),
		"page override beats both backend and defaults")
	require.Equal(t, 60*time.Second, pageInterval(be, cfg, "silences"),
		"page without override falls through to backend interval")
}

func TestPageInterval_FallsThroughToBackendThenDefaultsThenFloor(t *testing.T) {
	t.Parallel()

	// No page override, no per-backend interval, no default —
	// hits the 1-minute floor.
	cfgFloor := &config.Config{}
	require.Equal(t, time.Minute,
		pageInterval(config.Backend{Name: "x", URL: "http://x"}, cfgFloor, "alerts"))

	// Per-backend wins over defaults when no page override.
	cfgBE := &config.Config{Defaults: config.Defaults{PollInterval: 30 * time.Second}}
	be := config.Backend{Name: "x", URL: "http://x", PollInterval: 10 * time.Second}
	require.Equal(t, 10*time.Second, pageInterval(be, cfgBE, "alerts"))

	// Defaults win over floor when no page override and no
	// per-backend value.
	cfgDef := &config.Config{Defaults: config.Defaults{PollInterval: 15 * time.Second}}
	require.Equal(t, 15*time.Second,
		pageInterval(config.Backend{Name: "x", URL: "http://x"}, cfgDef, "alerts"))
}

func TestPageOverride_AllResources(t *testing.T) {
	t.Parallel()

	p := config.PageOverrides{
		Alerts:    config.PageConfig{PollInterval: 1 * time.Second},
		Silences:  config.PageConfig{PollInterval: 2 * time.Second},
		Groups:    config.PageConfig{PollInterval: 3 * time.Second},
		Receivers: config.PageConfig{PollInterval: 4 * time.Second},
		Status:    config.PageConfig{PollInterval: 5 * time.Second},
	}
	require.Equal(t, 1*time.Second, pageOverride(p, "alerts"))
	require.Equal(t, 2*time.Second, pageOverride(p, "silences"))
	require.Equal(t, 3*time.Second, pageOverride(p, "groups"))
	require.Equal(t, 4*time.Second, pageOverride(p, "receivers"))
	require.Equal(t, 5*time.Second, pageOverride(p, "status"))
	require.Zero(t, pageOverride(p, "unknown"),
		"unknown resource returns zero so caller falls through to backend")
}

// writeDefaultKeys is a tui_test.go-local helper that drops a
// `<dir>/keys/default.yaml` file with the given body. Mirrors the
// `writeKeys` helper in internal/config but lives here so the cmd-
// layer integration tests don't reach into another package's test
// fixtures. v0.0.1 only auto-loads the default profile, so that's
// the only one the cmd-layer wiring exercises.
func writeDefaultKeys(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.KeysDir), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, config.KeysDir, config.DefaultKeysProfile+".yaml"),
		[]byte(body), 0o600))
	return dir
}

// TestApplyUserKeyOverrides_EndToEnd exercises the load + apply path
// with a real `keys/default.yaml` against a dispatcher carrying a
// `quit` action. Pins the spec's load-bearing assertion: with the
// file in place the dispatcher honours capital `Q` for quit.
func TestApplyUserKeyOverrides_EndToEnd(t *testing.T) {
	t.Parallel()

	d := keys.New(nil)
	var fired atomic.Int32
	d.SetAction(keys.LayerGlobal, "quit", "q", func() tea.Cmd {
		fired.Add(1)
		return nil
	})

	dir := writeDefaultKeys(t, "quit: ['Q']\n")
	require.NoError(t, applyUserKeyOverrides(d, dir))

	// Both the default and the user-supplied key fire the same action.
	// `Q` in the YAML canonicalises to `Shift+Q` because bubbletea v2
	// reports a shifted letter as `Shift+Q` at runtime — dispatching
	// the bare `Q` would never fire the binding the user thought they
	// were adding.
	consumed, _ := d.Dispatch("q")
	require.True(t, consumed)
	consumed, _ = d.Dispatch("Shift+Q")
	require.True(t, consumed, "shift+q must fire the user-bound quit action")
	require.EqualValues(t, 2, fired.Load())
}

// TestApplyUserKeyOverrides_MissingFileIsNoError pins the "operators
// who don't curate keys see no mention of the feature" contract: the
// loader returns empty + nil, ApplyOverrides is skipped (length zero
// short-circuit), no error reaches the caller.
func TestApplyUserKeyOverrides_MissingFileIsNoError(t *testing.T) {
	t.Parallel()

	d := keys.New(nil)
	d.SetAction(keys.LayerGlobal, "quit", "q", func() tea.Cmd { return nil })

	require.NoError(t, applyUserKeyOverrides(d, t.TempDir()))
}

// TestApplyUserKeyOverrides_ReservedKeyFailsClosed pins the C3
// muscle-memory carve-out: a user file binding 0-9 must refuse to
// start with the precise file:line / reserved-keys error the spec
// quoted.
func TestApplyUserKeyOverrides_ReservedKeyFailsClosed(t *testing.T) {
	t.Parallel()

	d := keys.New(nil)
	d.SetAction(keys.LayerGlobal, "quit", "q", func() tea.Cmd { return nil })

	dir := writeDefaultKeys(t, "quit: ['3']\n")
	err := applyUserKeyOverrides(d, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "0-9 are reserved for tenant quick-switch")
}

// TestApplyUserKeyOverrides_UnknownActionFailsClosed pins the
// "typo'd action name fails closed" contract: better to refuse to
// start than to silently drop the binding the user thought they
// were adding.
func TestApplyUserKeyOverrides_UnknownActionFailsClosed(t *testing.T) {
	t.Parallel()

	d := keys.New(nil)
	d.SetAction(keys.LayerGlobal, "quit", "q", func() tea.Cmd { return nil })

	dir := writeDefaultKeys(t, "quitt: ['Q']\n")
	err := applyUserKeyOverrides(d, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown action "quitt"`)
}

// TestApplyUserKeyOverrides_SameFileConflictFailsClosed pins the
// loader's conflict check via the cmd-layer entry point. Surfaces
// file:line so the operator opens their editor at the right spot.
func TestApplyUserKeyOverrides_SameFileConflictFailsClosed(t *testing.T) {
	t.Parallel()

	d := keys.New(nil)
	d.SetAction(keys.LayerGlobal, "quit", "q", func() tea.Cmd { return nil })
	d.SetAction(keys.LayerGlobal, "refresh", "r", func() tea.Cmd { return nil })

	dir := writeDefaultKeys(t, "quit: ['Q']\nrefresh: ['Q']\n")
	err := applyUserKeyOverrides(d, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), `key "Shift+Q" is also bound to action "quit"`)
}

// newTestApp builds a minimal app.App for the quit-filter tests.
// Mirrors internal/tui/app's test helper but lives here so the
// filter (defined in cmd) can be exercised without exporting wiring
// internals.
func newTestAppForFilter(t *testing.T) *app.App {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return app.NewApp(app.Options{
		Styles:     styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
	})
}

// TestQuitFilter_TranslatesQuitMsgWhenAppNotQuitting pins Fix 1's
// SIGTERM-cascade contract: bubbletea's handleSignals pushes raw
// tea.QuitMsg into the message channel on SIGTERM, and the eventLoop
// short-circuits on QuitMsg BEFORE Update fires. Without the filter
// every page-stack Close (cancelBulk, cancelEditorUpdate, silence-form
// cancel funcs) leaks for the full HTTP timeout window on kubernetes
// / systemd shutdown. The filter must translate the raw QuitMsg into
// app.QuitRequestedMsg so the QuitRequestedMsg cascade runs.
func TestQuitFilter_TranslatesQuitMsgWhenAppNotQuitting(t *testing.T) {
	t.Parallel()
	a := newTestAppForFilter(t)
	filter := newQuitFilter(a)

	out := filter(a, tea.QuitMsg{})
	require.IsType(t, app.QuitRequestedMsg{}, out,
		"raw tea.QuitMsg (SIGTERM path) must translate to QuitRequestedMsg "+
			"so the page-stack Close cascade runs before bubbletea exits")
}

// TestQuitFilter_TranslatesInterruptMsgWhenAppNotQuitting pins the
// SIGINT companion: bubbletea pushes tea.InterruptMsg on SIGINT when
// the terminal is not in raw mode (non-TTY input, e.g. piped runs);
// the eventLoop short-circuits on InterruptMsg the same way. The
// filter routes it through QuitRequestedMsg too so the cleanup
// contract holds for both signals.
func TestQuitFilter_TranslatesInterruptMsgWhenAppNotQuitting(t *testing.T) {
	t.Parallel()
	a := newTestAppForFilter(t)
	filter := newQuitFilter(a)

	out := filter(a, tea.InterruptMsg{})
	require.IsType(t, app.QuitRequestedMsg{}, out,
		"raw tea.InterruptMsg (SIGINT path) must translate to QuitRequestedMsg")
}

// TestQuitFilter_PassesQuitMsgThroughWhenAppAuthorised verifies the
// other side of the contract: once the App has already run the
// cascade and emitted tea.Quit itself (via QuitRequestedMsg →
// quitWithCleanup → tea.Quit), the resulting QuitMsg MUST reach the
// runtime so the program actually exits. The handleLifecycle's
// tea.QuitMsg branch flips a.quitting before bubbletea reads the
// next message; the filter consults that flag and passes through
// unchanged. Without this gate the filter would loop QuitMsg back
// into QuitRequestedMsg forever and the program would never quit.
func TestQuitFilter_PassesQuitMsgThroughWhenAppAuthorised(t *testing.T) {
	t.Parallel()
	a := newTestAppForFilter(t)
	// Run a normal cascade to set a.quitting via the QuitMsg branch
	// in handleLifecycle. After this, tea.QuitMsg arriving at the
	// filter must pass through.
	a.Update(tea.QuitMsg{})
	require.True(t, a.Quitting(),
		"handleLifecycle must flip the quitting flag on tea.QuitMsg "+
			"so the filter has a stable signal to read")

	filter := newQuitFilter(a)
	out := filter(a, tea.QuitMsg{})
	require.IsType(t, tea.QuitMsg{}, out,
		"once the App authorised the quit, the filter must let "+
			"tea.QuitMsg through so bubbletea's eventLoop exits")
}

// TestQuitFilter_PassesNonQuitMsgsThrough verifies the filter only
// intercepts QuitMsg / InterruptMsg — every other tea.Msg must reach
// Update untouched. Without this guard the filter would silently
// swallow or rewrite WindowSizeMsg / KeyMsg / poll.DataMsg.
func TestQuitFilter_PassesNonQuitMsgsThrough(t *testing.T) {
	t.Parallel()
	a := newTestAppForFilter(t)
	filter := newQuitFilter(a)

	resize := tea.WindowSizeMsg{Width: 100, Height: 30}
	out := filter(a, resize)
	require.Equal(t, resize, out,
		"non-quit messages must pass through the filter unchanged")
}

// TestExecuteContext_CancelsOnContextDone pins Fix 2: cmd.Context()
// must become cancellable via signal.NotifyContext + ExecuteContext.
// Pages document that editorCtx / bulkCtx parents propagate app
// shutdown — but the contract was dead because cmd/cmd.go called
// rootCmd.Execute() so cmd.Context() was always context.Background().
// This test exercises the wiring with a no-op RunE and verifies that
// once the parent context is cancelled the command observes it via
// cmd.Context(). Real SIGTERM is exercised indirectly: signal.NotifyContext
// produces a context that cancels on the configured signals, and the
// only thing ExecuteContext is doing is plumbing that context through.
func TestExecuteContext_CancelsOnContextDone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pointer cell so the closure assigns into the outer slot — `var
	// observed context.Context` + `observed = cmd.Context()` reads
	// cleaner but trips the lint's shadow heuristic when the var is
	// declared at the test scope and assigned inside a nested closure.
	observed := make(chan context.Context, 1)
	var flags GlobalFlags
	root := newRootCmd(&flags, func(cmd *cobra.Command, _ *GlobalFlags) error {
		observed <- cmd.Context()
		return nil
	})
	root.SetArgs([]string{})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))

	// Cancel BEFORE ExecuteContext returns so the assertion below sees
	// the cancellation propagated through cmd.Context(). A real SIGTERM
	// path is the same shape: signal.NotifyContext cancels the parent,
	// every cmd.Context() reading code path observes it.
	cancel()

	require.NoError(t, root.ExecuteContext(ctx))
	got := <-observed
	require.NotNil(t, got, "RunE must run and capture cmd.Context()")
	require.ErrorIs(t, got.Err(), context.Canceled,
		"cmd.Context() must inherit cancellation from the parent passed to ExecuteContext")
}

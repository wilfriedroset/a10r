// SPDX-License-Identifier: Apache-2.0

package boot

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
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// TestLogTransportSurprises pins the startup transport-warning
// log surface. Sequential (no t.Parallel) because the proxy row
// mutates process-wide env via t.Setenv.
func TestLogTransportSurprises(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		backends   []config.Backend
		wantSubstr []string
		wantEmpty  bool
	}{
		{
			// TLS 1.0/1.1 stay in the schema as a connectivity escape
			// hatch for legacy backends, but every selection must emit
			// a WARN at startup so the operator sees the deprecation on
			// every run.
			name: "TLS10 min version emits warn",
			backends: []config.Backend{
				{Name: "legacy", TLSConfig: &config.TLSConfig{MinVersion: "TLS10"}},
			},
			wantSubstr: []string{"level=WARN", "min_version=TLS10", "backend=legacy"},
		},
		{
			// Keep the Prometheus-parity replace semantics but log INFO
			// once per backend so the operator sees that the inline CA
			// pinning is in effect (and the system root pool is bypassed).
			name: "inline CA emits info",
			backends: []config.Backend{
				{Name: "self-signed", TLSConfig: &config.TLSConfig{CA: "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----"}},
			},
			wantSubstr: []string{"level=INFO", "system CA roots not used", "backend=self-signed"},
		},
		{
			// A backend that opts into proxy_from_environment logs
			// which proxy URL the OS env actually resolves to, so the
			// operator can spot a HTTPS_PROXY hijack chain on startup.
			name: "proxy from environment logs resolved",
			env: map[string]string{
				"HTTPS_PROXY": "http://proxy.internal:3128",
				"HTTP_PROXY":  "http://proxy.internal:3128",
			},
			backends: []config.Backend{
				{Name: "via-proxy", URL: "https://am.example", ProxyFromEnvironment: true},
			},
			wantSubstr: []string{
				"level=INFO",
				"proxy_from_environment active",
				"backend=via-proxy",
				"proxy=http://proxy.internal:3128",
			},
		},
		{
			// Guards against noise creep: a backend without a TLS block
			// must not emit any log line.
			name: "no TLS stays silent",
			backends: []config.Backend{
				{Name: "plain"},
				{Name: "modern", TLSConfig: &config.TLSConfig{MinVersion: "TLS12"}},
			},
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			buf := &strings.Builder{}
			logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			logTransportSurprises(logger, tc.backends)

			if tc.wantEmpty {
				require.Empty(t, buf.String(), "no surprises in scope must produce no log lines")
				return
			}
			out := buf.String()
			for _, s := range tc.wantSubstr {
				require.Contains(t, out, s, "expected %q in log output", s)
			}
		})
	}
}

// TestLevelFor pins the CLI flag fold. The debug-wins rows matter
// because previously slog.Default() (stderr) was used regardless;
// a plumbed --debug must reach the file logger so debug records
// survive into the persistent audit trail.
func TestLevelFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		debug, quiet bool
		want         slog.Level
	}{
		{name: "default is info", debug: false, quiet: false, want: slog.LevelInfo},
		{name: "debug wins", debug: true, quiet: false, want: slog.LevelDebug},
		{name: "quiet drops to warn", debug: false, quiet: true, want: slog.LevelWarn},
		{name: "debug and quiet prefers debug", debug: true, quiet: true, want: slog.LevelDebug},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, LevelFor(tc.debug, tc.quiet))
		})
	}
}

// TestUserAgent pins the User-Agent header surface across the
// build variants a10r ships through.
func TestUserAgent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		version, commit string
		want            string
	}{
		// goreleaser-skipped local build (`go build` with no -X ldflags):
		// bare `a10r/dev`, no parenthesised commit suffix when commit is
		// the sentinel "none".
		{name: "dev build", version: "dev", commit: "none", want: "a10r/dev"},
		// goreleaser path: a non-default commit appears as a parenthesised
		// RFC 9110 comment suffix so backend operators can grep one
		// access-log line back to the exact build.
		{name: "release build", version: "1.2.3", commit: "abc1234", want: "a10r/1.2.3 (abc1234)"},
		// Defensive branch: an unset commit (empty string) folds into the
		// same branch as the sentinel "none" so neither variant produces
		// a stray "()" suffix.
		{name: "empty commit treated as none", version: "1.2.3", commit: "", want: "a10r/1.2.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, UserAgent(tc.version, tc.commit))
		})
	}
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

// TestBuildTenantRows_EmptyConfig covers the cold-start
// no-backend case — the wizard's pre-config state — to verify
// the helper doesn't panic on a zero Config.
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
			return backend.Status{}, ctx.Err()
		}
	}
	if f.err != nil {
		return backend.Status{}, f.err
	}
	return backend.Status{Version: backend.VersionInfo{Version: f.version}}, nil
}

func (*fakeStatusBackend) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
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

// TestFetchTenantVersions_AggregatesByBackendName covers the
// happy path: every client returns its version, the aggregated
// map keys match the configured backend names exactly.
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

	cfgFloor := &config.Config{}
	require.Equal(t, time.Minute,
		pageInterval(config.Backend{Name: "x", URL: "http://x"}, cfgFloor, "alerts"))

	cfgBE := &config.Config{Defaults: config.Defaults{PollInterval: 30 * time.Second}}
	be := config.Backend{Name: "x", URL: "http://x", PollInterval: 10 * time.Second}
	require.Equal(t, 10*time.Second, pageInterval(be, cfgBE, "alerts"))

	cfgDef := &config.Config{Defaults: config.Defaults{PollInterval: 15 * time.Second}}
	require.Equal(t, 15*time.Second,
		pageInterval(config.Backend{Name: "x", URL: "http://x"}, cfgDef, "alerts"))
}

func TestPageOverride_AllResources(t *testing.T) {
	t.Parallel()

	p := config.PageOverrides{
		Alerts:    config.PageConfig{PollInterval: 1 * time.Second},
		Silences:  config.PageConfig{PollInterval: 2 * time.Second},
		Receivers: config.PageConfig{PollInterval: 4 * time.Second},
		Status:    config.PageConfig{PollInterval: 5 * time.Second},
	}
	require.Equal(t, 1*time.Second, pageOverride(p, "alerts"))
	require.Equal(t, 2*time.Second, pageOverride(p, "silences"))
	require.Equal(t, 4*time.Second, pageOverride(p, "receivers"))
	require.Equal(t, 5*time.Second, pageOverride(p, "status"))
	require.Zero(t, pageOverride(p, "unknown"),
		"unknown resource returns zero so caller falls through to backend")
}

// writeDefaultKeys drops a `<dir>/keys/default.yaml` file with the
// given body. Only the default profile is auto-loaded today, so
// that's the only one the cmd-layer wiring exercises.
func writeDefaultKeys(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.KeysDir), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, config.KeysDir, config.DefaultKeysProfile+".yaml"),
		[]byte(body), 0o600,
	))
	return dir
}

// TestApplyUserKeyOverrides_EndToEnd exercises the load + apply
// path with a real `keys/default.yaml` against a dispatcher
// carrying a `quit` action.
func TestApplyUserKeyOverrides_EndToEnd(t *testing.T) {
	t.Parallel()

	d := keys.New(nil)
	var fired atomic.Int32
	d.SetAction(keys.LayerGlobal, "quit", "quit", "q", func() tea.Cmd {
		fired.Add(1)
		return nil
	})

	dir := writeDefaultKeys(t, "quit: ['Q']\n")
	require.NoError(t, applyUserKeyOverrides(d, dir, config.LoadKeys))

	// Both the default and the user-supplied key fire the same action.
	// `Q` in the YAML canonicalises to `Shift+Q` at runtime.
	consumed, _ := d.Dispatch("q")
	require.True(t, consumed)
	consumed, _ = d.Dispatch("Shift+Q")
	require.True(t, consumed, "shift+q must fire the user-bound quit action")
	require.EqualValues(t, 2, fired.Load())
}

// TestApplyUserKeyOverrides pins the boot-layer key-override
// surface: missing files are silent, and every fail-closed branch
// surfaces a precise error message.
func TestApplyUserKeyOverrides(t *testing.T) {
	t.Parallel()
	type extraAction struct{ id, key string }
	cases := []struct {
		name         string
		yaml         string
		extraActions []extraAction
		wantErr      string
	}{
		// Operators who don't curate keys see no mention of the feature.
		{name: "missing file is no error", yaml: ""},
		// C3 muscle-memory carve-out: a user file binding 0-9 must
		// refuse to start with the precise reserved-keys error.
		{name: "reserved key fails closed", yaml: "quit: ['3']\n", wantErr: "0-9 are reserved for tenant quick-switch"},
		// Typo'd action name fails closed.
		{name: "unknown action fails closed", yaml: "quitt: ['Q']\n", wantErr: `unknown action "quitt"`},
		{
			// Loader's conflict check via the boot-layer entry point.
			name:         "same-file conflict fails closed",
			yaml:         "quit: ['Q']\nrefresh: ['Q']\n",
			extraActions: []extraAction{{id: "refresh", key: "r"}},
			wantErr:      `key "Shift+Q" is also bound to action "quit"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := keys.New(nil)
			d.SetAction(keys.LayerGlobal, "quit", "quit", "q", func() tea.Cmd { return nil })
			for _, ea := range tc.extraActions {
				d.SetAction(keys.LayerGlobal, ea.id, ea.id, ea.key, func() tea.Cmd { return nil })
			}

			var dir string
			if tc.yaml == "" {
				dir = t.TempDir()
			} else {
				dir = writeDefaultKeys(t, tc.yaml)
			}

			err := applyUserKeyOverrides(d, dir, config.LoadKeys)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// newTestAppForFilter builds a minimal app.App for the
// quit-filter tests.
func newTestAppForFilter(t *testing.T) *app.App {
	t.Helper()
	styles := testutil.LoadStyles(t)
	return app.NewApp(app.Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
	})
}

// TestQuitFilter pins the pre-authorisation surface of the
// bubbletea quit filter: raw QuitMsg/InterruptMsg get translated
// to QuitRequestedMsg so the page-stack Close cascade runs, while
// unrelated messages pass through untouched.
func TestQuitFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		msg    tea.Msg
		wantIs tea.Msg
	}{
		// SIGTERM-cascade contract: raw tea.QuitMsg must translate to
		// QuitRequestedMsg so the page-stack Close cascade runs before
		// bubbletea exits.
		{name: "translates QuitMsg when app not quitting", msg: tea.QuitMsg{}},
		// SIGINT companion to the SIGTERM path above.
		{name: "translates InterruptMsg when app not quitting", msg: tea.InterruptMsg{}},
		// Pass-through scope: the filter only intercepts QuitMsg /
		// InterruptMsg; everything else flows untouched.
		{
			name:   "passes non-quit msgs through",
			msg:    tea.WindowSizeMsg{Width: 100, Height: 30},
			wantIs: tea.WindowSizeMsg{Width: 100, Height: 30},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newTestAppForFilter(t)
			filter := QuitFilter(a)

			out := filter(a, tc.msg)
			if tc.wantIs == nil {
				require.IsType(t, app.QuitRequestedMsg{}, out)
				return
			}
			require.Equal(t, tc.wantIs, out)
		})
	}
}

// TestQuitFilter_PassesQuitMsgThroughWhenAppAuthorised drives
// through the production-shaped sequence and asserts the filter
// passes QuitMsg through unchanged once authorised.
func TestQuitFilter_PassesQuitMsgThroughWhenAppAuthorised(t *testing.T) {
	t.Parallel()
	a := newTestAppForFilter(t)

	_, cmd := a.Update(app.QuitRequestedMsg{})
	require.NotNil(t, cmd, "QuitRequestedMsg must produce a follow-up Cmd")

	msg := cmd()
	require.NotNil(t, msg,
		"the cleanup batch must ultimately emit a tea.QuitMsg")

	filter := QuitFilter(a)
	out := filter(a, msg)
	require.IsType(t, tea.QuitMsg{}, out,
		"once quitWithCleanup has authorised the quit, the filter must "+
			"let tea.QuitMsg through so bubbletea's eventLoop exits")
}

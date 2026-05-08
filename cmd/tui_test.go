// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
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

// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"errors"
	"io"
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
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// testDeps returns a Deps populated with fakes that let Build run
// end-to-end without touching the filesystem (other than t.TempDir
// when callers need it) or spawning real network clients. Callers
// override individual fields to drive specific behaviour.
func testDeps(t *testing.T) Deps {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)

	return Deps{
		LoadConfig: func(_ config.LoadOpts) (*config.Config, error) {
			return &config.Config{}, nil
		},
		NewLogger: func(_ a10rlog.Opts) (*slog.Logger, io.Closer, error) {
			return slog.New(slog.DiscardHandler), io.NopCloser(strings.NewReader("")), nil
		},
		BuildClient: func(_ config.Backend, _ string, _ ...factory.Option) (backend.Client, error) {
			return &fakeStatusBackend{}, nil
		},
		LoadStyles: func(_, _ string) (*theme.Styles, error) {
			return styles, nil
		},
		LoadAliases: func(string) (config.AliasMap, error) {
			return config.AliasMap{}, nil
		},
		LoadKeys: func(string, string) (config.KeyOverrides, error) {
			return config.KeyOverrides{}, nil
		},
		ResolveConfigDir: func(string) (string, error) {
			return t.TempDir(), nil
		},
		EditorResolver: edit.SystemResolver,
		HistoryDir: func() (string, error) {
			return "", nil
		},
		Version: "test",
		Commit:  "deadbeef",
		Stderr:  io.Discard,
	}
}

// TestBuild_HappyPathReturnsApp asserts the cold-start cold-config
// case: zero backends, every Dep is a fake, Build still produces a
// usable Result with a non-nil App and a working Closer.
func TestBuild_HappyPathReturnsApp(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)

	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.App(), "Result.App() must return the assembled bubbletea Model")
	require.NoError(t, res.Close(), "Close() on the cold-start path must succeed")
}

// TestBuild_LoadConfigErrorPropagates pins the precondition that
// Stage 1's config parsing is load-bearing — a malformed file
// must fail the whole boot rather than degrading silently.
// ErrNotFound is the only tolerated outcome (cold-start path).
func TestBuild_LoadConfigErrorPropagates(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	want := errors.New("parse a10r.yaml: invalid yaml at line 4")
	deps.LoadConfig = func(_ config.LoadOpts) (*config.Config, error) {
		return nil, want
	}

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.ErrorIs(t, err, want,
		"Stage 1 must propagate a non-ErrNotFound config error verbatim "+
			"so the operator sees the parse failure on startup")
}

// TestBuild_LoadConfigErrNotFoundIsTolerated pins the cold-start
// path: ErrNotFound is not an error — Build degrades to an empty
// Config and proceeds so the wizard hint reaches the user.
func TestBuild_LoadConfigErrNotFoundIsTolerated(t *testing.T) {
	t.Parallel()
	stderr := &strings.Builder{}
	deps := testDeps(t)
	deps.Stderr = stderr
	deps.LoadConfig = func(_ config.LoadOpts) (*config.Config, error) {
		return nil, config.ErrNotFound
	}

	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err, "ErrNotFound is the wizard-precursor path; Build must proceed")
	require.NotNil(t, res.App())
	require.Contains(t, stderr.String(), "no config found",
		"cold-start path must surface the wizard hint to stderr so the operator "+
			"knows the next step")
}

// TestBuild_LoggerFactoryErrorAborts pins the precondition that
// Stage 3 is hard-required: a logger init failure aborts startup
// (we don't have anywhere to emit subsequent audit records).
func TestBuild_LoggerFactoryErrorAborts(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	want := errors.New("logger init failed: permission denied")
	deps.NewLogger = func(_ a10rlog.Opts) (*slog.Logger, io.Closer, error) {
		return nil, nil, want
	}

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.ErrorIs(t, err, want, "Stage 3 must propagate a logger factory failure")
}

// TestBuild_ResolveConfigDirErrorAborts pins Stage 7's hard
// dependency on configDir — the theme loader, key loader, and
// alias loader all read from it.
func TestBuild_ResolveConfigDirErrorAborts(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	want := errors.New("XDG_CONFIG_HOME points at a non-directory")
	deps.ResolveConfigDir = func(string) (string, error) { return "", want }

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.ErrorIs(t, err, want)
}

// TestBuild_ThemeLoadErrorAborts pins that an unparseable theme
// is fatal — a startup with no theme is unusable and we'd rather
// fail loudly than render a placeholder.
func TestBuild_ThemeLoadErrorAborts(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	want := errors.New("skin yaml is malformed")
	deps.LoadStyles = func(_, _ string) (*theme.Styles, error) { return nil, want }

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.ErrorIs(t, err, want)
}

// TestBuild_UserAliasesErrorAborts pins the "user aliases fail
// closed" contract: a malformed aliases file must fail startup,
// not silently drop the alias.
func TestBuild_UserAliasesErrorAborts(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	want := errors.New("aliases.yaml line 3: invalid expansion")
	deps.LoadAliases = func(string) (config.AliasMap, error) { return nil, want }

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.ErrorIs(t, err, want)
	require.Contains(t, err.Error(), "user aliases:",
		"the error wrap must scope the failure so the operator finds the file")
}

// TestBuild_UserKeysErrorAborts pins the "user keybindings fail
// closed" contract (ADR 0010).
func TestBuild_UserKeysErrorAborts(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	want := errors.New("keys/default.yaml: unknown action quitt")
	deps.LoadKeys = func(string, string) (config.KeyOverrides, error) { return nil, want }

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.ErrorIs(t, err, want)
	require.Contains(t, err.Error(), "user keybindings:")
}

// TestBuild_BuildClientFailuresAreNonFatal pins the
// per-backend resilience contract: one failing client must NOT
// abort the rest of startup; the misconfigured entry surfaces as
// a stderr warning and the page factories keep the rest.
func TestBuild_BuildClientFailuresAreNonFatal(t *testing.T) {
	t.Parallel()
	stderr := &strings.Builder{}
	deps := testDeps(t)
	deps.Stderr = stderr
	deps.LoadConfig = func(_ config.LoadOpts) (*config.Config, error) {
		return &config.Config{
			Backends: []config.Backend{
				{Name: "good", URL: "http://good"},
				{Name: "bad", URL: "http://bad"},
			},
		}, nil
	}
	deps.BuildClient = func(cfg config.Backend, _ string, _ ...factory.Option) (backend.Client, error) {
		if cfg.Name == "bad" {
			return nil, errors.New("connection refused")
		}
		return &fakeStatusBackend{version: "0.27.0"}, nil
	}

	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err, "one bad backend must not abort the whole boot")
	require.NotNil(t, res.App())
	require.Contains(t, stderr.String(), `backend "bad": build failed`,
		"the misconfigured entry must surface the warning to stderr "+
			"so the operator sees it without scanning the audit log")
}

// TestBuild_DefaultsLandWhenDepsIsZero asserts the production-
// default contract from Deps.resolved: passing a zero Deps still
// produces non-nil results (the constructors point at real
// implementations). The boot succeeds only when LoadConfig
// returns ErrNotFound — we don't want the test to read any real
// XDG paths — so we wire the minimum override.
func TestBuild_DefaultsLandWhenDepsIsZero(t *testing.T) {
	// Sequential — Setenv mutates process-wide env.
	// Force the test to read no production paths.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	_, err := Build(t.Context(), &config.CLIFlags{ConfigDir: t.TempDir()}, Deps{
		Stderr: io.Discard,
	})
	require.NoError(t, err, "zero Deps must fold into production defaults that succeed on a cold-start path")
}

// TestBuild_VersionAndCommitDefaultToSentinelStrings pins that
// the zero-value Version / Commit fold into the "dev" / "none"
// sentinels via Deps.resolved so the User-Agent stays
// well-formed even when the caller forgets to pass build vars.
func TestBuild_VersionAndCommitDefaultToSentinelStrings(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	deps.Version = ""
	deps.Commit = ""

	// Capture the UA via BuildClient.
	var captured string
	deps.BuildClient = func(_ config.Backend, ua string, _ ...factory.Option) (backend.Client, error) {
		captured = ua
		return &fakeStatusBackend{}, nil
	}
	deps.LoadConfig = func(_ config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{{Name: "x", URL: "http://x"}}}, nil
	}

	_, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	require.Equal(t, "a10r/dev", captured,
		"zero version/commit must fold into the sentinel UA so backend "+
			"operators don't see an empty `a10r/ ()` token")
}

// TestResult_StartPollersCleansUpOnStop pins the lifecycle
// contract: StartPollers returns a stop func that drains every
// spawned goroutine. Even with no backends (zero clients), the
// stop func is callable as a no-op so cmd/tui.go's defer is safe.
func TestResult_StartPollersCleansUpOnStop(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)

	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)

	sent := make(chan struct{}, 1)
	send := func(_ tea.Msg) {
		select {
		case sent <- struct{}{}:
		default:
		}
	}
	stop := res.StartPollers(t.Context(), send)
	require.NotNil(t, stop, "StartPollers must return a non-nil stop even with zero clients")
	stop() // must not panic; must be idempotent enough to defer.
}

// TestResult_PushHomeShortCircuitsCancelledCtx pins the
// defence-in-depth contract for the home-push goroutine: a Ctrl+C
// between Run start and the first Send must not call send on a
// disposed program.
func TestResult_PushHomeShortCircuitsCancelledCtx(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	ctx, cancel := context.WithCancel(context.Background())

	res, err := Build(ctx, &config.CLIFlags{}, deps)
	require.NoError(t, err)
	cancel()

	called := atomic.Int32{}
	send := func(_ tea.Msg) { called.Add(1) }
	res.PushHome(ctx, send)

	// Allow the goroutine to settle. The ctx is already cancelled
	// so the short-circuit must fire before send runs.
	time.Sleep(20 * time.Millisecond)
	require.Zero(t, called.Load(),
		"cancelled ctx must short-circuit the home-push send so a Ctrl+C "+
			"between Build and Run does not crash the disposed program")
}

// TestBuild_LoadOptsFromFlagsForwardsConfigPath pins Stage 1's
// flag-to-LoadOpts mapping: an explicit --config path must split
// into Dir + File so the loader reads the requested file rather
// than the XDG default.
func TestBuild_LoadOptsFromFlagsForwardsConfigPath(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "custom.yaml")
	require.NoError(t, os.WriteFile(path, []byte("backends: []"), 0o600))

	var captured config.LoadOpts
	deps := testDeps(t)
	deps.LoadConfig = func(opts config.LoadOpts) (*config.Config, error) {
		captured = opts
		return &config.Config{}, nil
	}

	flags := &config.CLIFlags{ConfigPath: path}
	_, err := Build(t.Context(), flags, deps)
	require.NoError(t, err)
	require.Equal(t, tmpDir, captured.Dir)
	require.Equal(t, "custom.yaml", captured.File)
}

// SPDX-License-Identifier: Apache-2.0

// Package boot assembles the TUI's startup graph: parse config,
// build the logger, build backend clients, fetch tenant versions,
// load styles, register key overrides + aliases, construct the
// resolver and page factories, and assemble the bubbletea Model.
// The entrypoint is Build; everything else in the package is
// internal scaffolding for the stages it sequences.
//
// The package keeps cmd/tui.go's runTUI a thin shell. Build's
// body reads top-to-bottom as a stage list with one block comment
// per stage describing the load-bearing precondition; the helpers
// live next to the stage that calls them so the orchestration is
// scannable without paging through helper noise.
//
// Build does not start the bubbletea program or push the home
// page — those need *tea.Program which is created after Build
// returns in cmd/tui.go. Build does produce everything the program
// needs: the App, the page-environment-bound resolver, the poller
// registry hooked into the App's Refresh handler, and the
// fetcher / interval / scope plumbing the wiring layer needs to
// spawn the poller goroutines once the program is built.
package boot

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenant"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Result bundles the post-Build state the wiring layer (cmd/tui.go)
// needs to finish startup: spawn the poller goroutines (which need
// prog.Send and so cannot start inside Build), and push the home
// page onto the stack once tea.Program is running.
//
// Every field is unexported — callers reach the side-effects via
// the App / StartPollers / PushHome / Close methods, never the
// raw inputs. The caller's contract is: build, defer Close, create
// program, call StartPollers(ctx, prog.Send), call PushHome(ctx,
// prog.Send), call program.Run.
type Result struct {
	app      *app.App
	closer   io.Closer
	cfg      *config.Config
	clients  map[string]backend.Client
	registry *pollerRegistry
	env      *pageEnv
	stderr   io.Writer
}

// App returns the bubbletea Model that tea.NewProgram wraps.
func (r *Result) App() *app.App { return r.app }

// Close flushes the logger sink. Wired by cmd/tui.go's `defer
// closer.Close()`. The method satisfies io.Closer; the error is
// surfaced as a warning on stderr inside closeLogger (the program
// is already exiting, so escalation has nothing to offer).
func (r *Result) Close() error {
	closeLogger(r.closer, r.stderr)
	return nil
}

// StartPollers spawns the per-(backend, resource) poller matrix.
// Called by cmd/tui.go after tea.NewProgram returns so each poller
// has a live prog.Send to push DataMsg into the update loop. ctx
// is the caller's program-scoped context — pollers stop when it
// cancels, in addition to honouring the returned stop func. The
// stop is safe to call zero or one times.
func (r *Result) StartPollers(ctx context.Context, send func(tea.Msg)) func() {
	return startBackendPoller(ctx, r.cfg, r.clients, send, r.registry)
}

// PushHome enqueues the alerts home page onto the App's stack via
// the resolver-backed page factory. The push is deferred behind a
// goroutine so a Ctrl+C between Run start and the first Send
// doesn't leak the goroutine for the rest of the session —
// defence-in-depth for multi-day usage. ctx is the caller's
// program-scoped context — a cancellation between Build and Run
// short-circuits the send call on the disposed program.
func (r *Result) PushHome(ctx context.Context, send func(tea.Msg)) {
	env := r.env
	go func() {
		homeFactory := func() app.Page { return newAlertsPage(env, "", "") }
		msg := app.PushPage(homeFactory)()
		if ctx.Err() != nil {
			return
		}
		send(msg)
	}()
}

// Build assembles every startup dependency the TUI needs and
// returns a Result the wiring layer (cmd/tui.go) uses to finish
// the boot sequence. Sequential body; each step is a named helper
// that documents its own precondition. See ADR 0033.
//
// The flags pointer is read for precedence (CLI > env > config >
// default) via config.Resolve; Build does not mutate it. The Deps
// zero value picks production defaults via Deps.resolved.
func Build(ctx context.Context, flags *config.CLIFlags, deps Deps) (*Result, error) {
	d := deps.resolved()
	errOut := d.Stderr
	if errOut == nil {
		errOut = os.Stderr
	}

	cfg, err := loadConfigForTUI(flags, d.LoadConfig, errOut)
	if err != nil {
		return nil, err
	}
	effective, err := resolveEffectiveConfig(flags, cfg)
	if err != nil {
		return nil, err
	}
	effCfg := effective.Config

	logger, closer, err := initLogger(d, effCfg, effective)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger)

	logTransportSurprises(logger, effCfg.Backends)

	clients, silenceClients := buildBackendClients(flags, logger, d, &effCfg, errOut)
	tenantRows := buildTenantRows(&effCfg, fetchTenantVersions(ctx, clients))

	configDir, styles, err := resolveConfigDirAndStyles(d, flags.ConfigDir, effCfg.Theme.Name)
	if err != nil {
		return nil, err
	}

	dispatcher := buildDispatcher()

	// buildPageEnv → buildApp dance: pageEnv's TimeFormat closure
	// needs the live *app.App, but app.NewApp itself consumes env-
	// derived values. Forward-declare the pointer, build env around
	// it, then assign in buildApp — closures resolve `a` at
	// invocation time, which is after buildApp has returned.
	var a *app.App
	env, resolver, err := buildPageEnv(ctx, &effCfg, styles, silenceClients, tenantRows, clients, d, &a, configDir)
	if err != nil {
		return nil, err
	}

	registry := &pollerRegistry{}
	a = buildApp(dispatcher, resolver, styles, &effCfg, registry, d)

	if err := applyUserKeyOverrides(dispatcher, configDir, d.LoadKeys); err != nil {
		return nil, fmt.Errorf("user keybindings: %w", err)
	}

	return &Result{
		app:      a,
		closer:   closer,
		cfg:      cfg,
		clients:  clients,
		registry: registry,
		env:      env,
		stderr:   errOut,
	}, nil
}

// resolveEffectiveConfig folds CLI > env > config > defaults into a
// single Effective. Bypassing the resolver silently dropped
// --read-only / --theme / --poll-interval pre-extraction; it stays
// mandatory, not an optimisation.
func resolveEffectiveConfig(flags *config.CLIFlags, cfg *config.Config) (config.Effective, error) {
	eff, err := config.Resolve(*flags, os.Getenv, *cfg)
	if err != nil {
		return config.Effective{}, fmt.Errorf("resolve config: %w", err)
	}
	return eff, nil
}

// initLogger initialises the project logger before any subsystem can
// emit so silence write ops produce an audit trail and --log actually
// reaches the file. The closer is returned so the caller's defer
// Close flushes the lumberjack rotation buffer on shutdown.
func initLogger(d Deps, effCfg config.Config, eff config.Effective) (*slog.Logger, io.Closer, error) {
	logger, closer, err := d.NewLogger(a10rlog.Opts{
		Path:   effCfg.Log.Path,
		Format: a10rlog.Format(effCfg.Defaults.LogFormat),
		Level:  LevelFor(eff.Debug, eff.Quiet),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("init logger: %w", err)
	}
	return logger, closer, nil
}

// buildBackendClients constructs the per-backend clients shared between
// the poller fan-out (read paths) and the page factories (write
// paths). Per-backend factory failures log a warning and are skipped —
// the rest still get clients. debugLog is non-nil only when
// --debug-http is set so factory.WithDebugLog wires the HTTP record
// sink.
func buildBackendClients(flags *config.CLIFlags, logger *slog.Logger, d Deps, effCfg *config.Config, errOut io.Writer) (clients map[string]backend.Client, silenceClients map[string]silenceform.Client) {
	var debugLog *slog.Logger
	if flags.DebugHTTP {
		debugLog = logger
	}
	ua := UserAgent(d.Version, d.Commit)
	clients = buildClients(effCfg, ua, debugLog, d.BuildClient, errOut)
	silenceClients = silenceClientsFrom(clients)
	return clients, silenceClients
}

// resolveConfigDirAndStyles resolves the XDG/CLI config root used by
// every later filesystem read (skins, keys, aliases) and loads the
// theme. The theme loader runs after the logger is installed so its
// fallback warning emits on the audited sink.
func resolveConfigDirAndStyles(d Deps, explicitDir, themeName string) (string, *theme.Styles, error) {
	configDir, err := d.ResolveConfigDir(explicitDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve config dir: %w", err)
	}
	styles, err := d.LoadStyles(themeName, configDir)
	if err != nil {
		return "", nil, err //nolint:wrapcheck // LoadStyles already wraps with the skin path.
	}
	return configDir, styles, nil
}

// buildDispatcher constructs the keys.Dispatcher and registers global
// chords. Chord registration must happen before app.NewApp so the
// App's pre-built layers see them on the first key event; building
// the app first and registering chords after would race the user's
// first keystroke.
func buildDispatcher() *keys.Dispatcher {
	dispatcher := keys.New(nil)
	registerGlobalChords(dispatcher)
	return dispatcher
}

// buildPageEnv assembles the pageEnv every page needs at push time
// plus the cmdbar resolver registering `:command` handlers that close
// over env. The appPtr forward-reference lets the TimeFormat closure
// see the live *app.App once buildApp assigns to it — pages pushed
// after the user toggles `t` then read the current app-global value.
// User aliases are overlaid here too; conflicts fail closed at
// startup so the operator sees the problem before they reach for the
// alias.
func buildPageEnv(ctx context.Context, effCfg *config.Config, styles *theme.Styles, silenceClients map[string]silenceform.Client, tenantRows []tenant.Row, clients map[string]backend.Client, d Deps, appPtr **app.App, configDir string) (*pageEnv, *cmdbar.Resolver, error) {
	timeFormat := func() timerender.Format {
		if *appPtr == nil {
			return timerender.Relative
		}
		return (*appPtr).TimeFormat()
	}
	stateFormat := func() stateformat.Format {
		if *appPtr == nil {
			return stateformat.Full
		}
		return (*appPtr).StateFormat()
	}
	env := &pageEnv{
		EditorCtx:          ctx,
		Styles:             styles,
		Scope:              scopeFor(effCfg),
		SilenceClients:     silenceClients,
		Creator:            os.Getenv("USER"),
		TenantRows:         tenantRows,
		Config:             effCfg,
		Clients:            clients,
		TimeFormat:         timeFormat,
		StateFormat:        stateFormat,
		ReadOnly:           effCfg.Defaults.ReadOnly,
		TenantNames:        backendNames(effCfg),
		TenantConfigByName: tenantConfigIndex(effCfg),
		EditorResolver:     d.EditorResolver(),
	}
	resolver := newResolver(env)
	if _, err := registerUserAliases(resolver, configDir, d.LoadAliases); err != nil {
		return nil, nil, fmt.Errorf("user aliases: %w", err)
	}
	return env, resolver, nil
}

// buildApp constructs the bubbletea Model around the dispatcher,
// resolver, styles, and the registry's Refresh handler. registry is
// captured by pointer so the App's Refresh callback finds the live
// pollers once Result.StartPollers fills the registry in (the user
// can only press `r` after Run starts, which is after StartPollers
// has settled).
func buildApp(dispatcher *keys.Dispatcher, resolver *cmdbar.Resolver, styles *theme.Styles, effCfg *config.Config, registry *pollerRegistry, d Deps) *app.App {
	historyDir, _ := d.HistoryDir() // best-effort; empty disables persistence per ADR.
	return app.NewApp(app.Options{
		Styles:     styles,
		Dispatcher: dispatcher,
		CmdBar:     resolver,
		Tenants:    backendNames(effCfg),
		Refresh:    registry.Refresh,
		ReadOnly:   effCfg.Defaults.ReadOnly,
		HistoryDir: historyDir,
		HintBar: footer.NewHintBar(footer.HintBarOptions{
			Enabled:  effCfg.TUI.Tips,
			Interval: effCfg.TUI.TipsInterval,
		}),
	})
}

// registerGlobalChords wires the dispatcher entries that must
// exist before the App is built so every layer sees them on the
// first key event.
//
//   - `gg` is a chord (LayerTable). The dispatcher buffers the
//     first `g` and fires the registered handler on the second
//     within 500 ms. LayerTable means every table-bodied page
//     (alerts, silences, receivers, groups, tenant) honours it
//     without per-page chord plumbing.
//
//   - `Ctrl+\` is LayerGlobal so it works regardless of which
//     list page is on top. The key string must match the App's
//     normalizeKey output (TitleCase modifier per keys.go:69) —
//     every other Ctrl binding in the codebase uses the same
//     `Ctrl+...` shape.
func registerGlobalChords(d *keys.Dispatcher) {
	d.Set(keys.LayerTable, "gg", func() tea.Cmd {
		return func() tea.Msg { return app.GoToFirstRowMsg{} }
	})
	d.Set(keys.LayerGlobal, "Ctrl+\\", func() tea.Cmd {
		return func() tea.Msg { return app.ClearMarksMsg{} }
	})
}

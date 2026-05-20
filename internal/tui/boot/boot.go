// SPDX-License-Identifier: Apache-2.0

// Package boot assembles the TUI's startup graph: parse config,
// build the logger, build backend clients, fetch tenant versions,
// load styles, register key overrides + aliases, construct the
// resolver and page factories, and assemble the bubbletea Model.
// The entrypoint is Build; everything else in the package is
// internal scaffolding for the stages it sequences.
//
// The package exists to keep cmd/tui.go's runTUI a thin shell:
// before this extraction, runTUI carried ~28 helpers and the
// precondition order between them was encoded only by call
// sequence in one ~250-line function. Build's body now reads
// top-to-bottom as a stage list with one block comment per stage
// describing the load-bearing precondition; the helpers live next
// to the stage that calls them so a future contributor can scan
// the orchestration without paging through helper noise.
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
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
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
// the boot sequence. Sequential body — one block comment per
// stage stating the load-bearing precondition so a future
// contributor can skim top-to-bottom without re-deriving the
// implicit order from call sequence.
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

	// Stage 1 — Config load. Required by every later stage:
	// backend list, logger path, theme name, poll intervals all
	// derive from the resolved config. Missing file is not an
	// error (cold-start + wizard path), but a malformed file is
	// fatal — startup must fail loudly rather than silently
	// dropping backends.
	cfg, err := loadConfigForTUI(flags, d.LoadConfig, errOut)
	if err != nil {
		return nil, err
	}

	// Stage 2 — Precedence resolution. Folds CLI > env > config >
	// defaults into a single Effective so every downstream
	// consumer (logger, theme loader, poll interval, page
	// ReadOnly gate) reads the same value. Bypassing this step
	// silently dropped --read-only / --theme / --poll-interval —
	// the resolver is mandatory, not an optimisation.
	effective, err := config.Resolve(*flags, os.Getenv, *cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve config: %w", err)
	}
	effCfg := effective.Config

	// Stage 3 — Logger. Initialised before any subsystem can
	// emit so silence write ops produce an audit trail and --log
	// actually reaches the file. The closer is returned in Result
	// so the caller's `defer Close` flushes the lumberjack
	// rotation buffer on shutdown.
	logger, closer, err := d.NewLogger(a10rlog.Opts{
		Path:   effCfg.Log.Path,
		Format: a10rlog.Format(effCfg.Defaults.LogFormat),
		Level:  LevelFor(effective.Debug, effective.Quiet),
	})
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}
	slog.SetDefault(logger)

	// Stage 4 — Transport surprises. Emitted once per startup so
	// the operator sees deprecated TLS versions / inline CA
	// override / resolved HTTPS_PROXY on every run instead of
	// inheriting them silently. Must run after the logger is
	// installed; harmless before any HTTP traffic.
	logTransportSurprises(logger, effCfg.Backends)

	// Stage 5 — Backend clients. Built once and shared between
	// the poller fan-out (read paths) and the page factories
	// (write paths) so the two stay in sync. Per-backend factory
	// failures log a warning and are skipped — the rest still
	// get clients. debugLog is non-nil only when --debug-http is
	// set so factory.WithDebugLog wires the HTTP record sink.
	var debugLog *slog.Logger
	if flags.DebugHTTP {
		debugLog = logger
	}
	ua := UserAgent(d.Version, d.Commit)
	clients := buildClients(&effCfg, ua, debugLog, d.BuildClient, errOut)
	silenceClients := silenceClientsFrom(clients)

	// Stage 6 — Tenant versions. One /api/v2/status call per
	// backend at startup so the tenant page can render a VERSION
	// column without a separate per-(backend, status) poller.
	// Concurrent fan-out so a slow backend doesn't block startup;
	// per-backend timeout caps each call.
	tenantVersions := fetchTenantVersions(ctx, clients)
	tenantRows := buildTenantRows(&effCfg, tenantVersions)

	// Stage 7 — Config-dir + theme. configDir is the resolved
	// XDG/CLI root used by every later filesystem read (skins,
	// keys, aliases). The theme loader runs after the logger is
	// installed so its fallback-warning emits on the audited
	// sink.
	configDir, err := d.ResolveConfigDir(flags.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}
	styles, err := d.LoadStyles(effCfg.Theme.Name, configDir)
	if err != nil {
		return nil, err //nolint:wrapcheck // LoadStyles already wraps with the skin path.
	}

	// Stage 8 — Dispatcher + global chords. Registered before
	// App.NewApp so the App's pre-built layers see them on the
	// first key event. Building the app first and registering
	// chords after would race the user's first keystroke.
	dispatcher := keys.New(nil)
	registerGlobalChords(dispatcher)

	// Stage 9 — pageEnv + resolver + user aliases. pageEnv
	// carries the construction-time deps every page needs; the
	// resolver registers `:command` handlers that close over
	// env. The `a` pointer is forward-declared and assigned by
	// stage 10 — closures read it at invocation time, by which
	// point app.NewApp has run, so the TimeFormat callback sees
	// the live app-global value (issue raised in the post-batch
	// review: pages pushed *after* the user toggled `t` were
	// opening in relative).
	creator := os.Getenv("USER")
	readOnly := effCfg.Defaults.ReadOnly
	var a *app.App
	timeFormat := func() timerender.Format {
		if a == nil {
			return timerender.Relative
		}
		return a.TimeFormat()
	}
	env := &pageEnv{
		EditorCtx:          ctx,
		Styles:             styles,
		Scope:              scopeFor(&effCfg),
		SilenceClients:     silenceClients,
		Creator:            creator,
		TenantRows:         tenantRows,
		Config:             &effCfg,
		Clients:            clients,
		TimeFormat:         timeFormat,
		ReadOnly:           readOnly,
		TenantNames:        backendNames(&effCfg),
		TenantConfigByName: tenantConfigIndex(&effCfg),
		EditorResolver:     d.EditorResolver(),
	}
	resolver := newResolver(env)
	// Overlay user aliases (G3): aliases.yaml is an optional
	// user-supplied {short -> expanded} map. Conflicts and
	// unresolved expansions fail closed at startup so the
	// operator sees the problem before they reach for the alias.
	if _, err := registerUserAliases(resolver, configDir, d.LoadAliases); err != nil {
		return nil, fmt.Errorf("user aliases: %w", err)
	}

	// Stage 10 — App. NewApp assembles the bubbletea Model
	// around the dispatcher, resolver, styles, and the registry's
	// Refresh handler. The registry is constructed empty here and
	// filled in by Result.StartPollers — captured by pointer so
	// the App's Refresh callback finds the live pollers once
	// they're added (the user can only press `r` after Run
	// starts, which is after StartPollers has settled).
	registry := &pollerRegistry{}
	historyDir, _ := d.HistoryDir() // best-effort; empty disables persistence per ADR.
	a = app.NewApp(app.Options{
		Styles:     styles,
		Dispatcher: dispatcher,
		CmdBar:     resolver,
		Tenants:    backendNames(&effCfg),
		Refresh:    registry.Refresh,
		ReadOnly:   readOnly,
		HistoryDir: historyDir,
		HintBar: footer.NewHintBar(footer.HintBarOptions{
			Enabled:  effCfg.TUI.Tips,
			Interval: effCfg.TUI.TipsInterval,
		}),
	})

	// Stage 11 — User key overrides. Loaded AFTER NewApp so the
	// dispatcher has every built-in action registered before
	// ApplyOverrides looks them up; failures fail-closed at
	// startup so the operator can't run with a half-applied
	// profile (P2.W1.5 / ADR 0010).
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

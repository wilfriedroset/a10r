// SPDX-License-Identifier: Apache-2.0

// Package app assembles the bubbletea program for a10r: it owns
// the root tea.Model, frames the screen as header/body/footer, and
// routes messages between the dispatcher, header, body, and footer
// subcomponents.
//
// The package is laid out across four files:
//
//   - app.go (this file) — Options, App struct, NewApp constructor,
//     simple accessors, Init.
//   - lifecycle.go — Update + msg routing, page-stack push/pop/
//     replace, poll-data cache, crumb refresh, top-of-stack helpers.
//   - input.go — keybinding registration (global + tenant), prompt
//     wiring, help / picker openers, the key-press dispatch path.
//   - view.go — View, panel state, body / footer composition,
//     small render helpers.
//
// Page interface, page-stack messages and TimeFormatChangedMsg live
// in page.go (the time-format vocabulary itself lives in timerender);
// modal slot messages in modal.go; help slot messages in help.go;
// key-name normalisation in keys.go.
package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Options collects the dependencies the App needs to operate.
// Exposed as a struct so the constructor stays additive: a future
// dependency (clock, browser, clipboard, logger) lands as a new
// field without touching every test that builds an App.
type Options struct {
	// Styles is the compiled theme. Re-rendered on every View call so
	// a future :theme command can hot-swap by replacing this field.
	Styles *theme.Styles
	// Dispatcher routes key events through the precedence stack. The
	// app shell pre-populates the global layer in NewApp.
	Dispatcher *keys.Dispatcher
	// CmdBar resolves `:` command-bar aliases to tea.Cmds. Optional —
	// nil falls back to a freshly-constructed empty resolver, in
	// which case every `:command` flashes "unknown command". Pages
	// and the wiring layer (cmd/tui.go) populate the resolver before
	// the program runs.
	CmdBar *cmdbar.Resolver
	// Tenants is the list of configured backend names. Used by
	// the top panel to render the `<0> all <1> name <2> name …`
	// tenant shortcut column k9s-style.
	Tenants []string
	// Refresh is the handler the App calls when a page emits a
	// RefreshRequestedMsg. The wiring layer wires it to walk the
	// (resource, scope) tuple and Refresh() each matching poller.
	// Optional: nil falls back to a no-op so headless tests don't
	// need to inject a dummy handler.
	Refresh func(resource, scope string)
	// ReadOnly is the resolved defaults.read_only / --read-only /
	// A10R_READ_ONLY value. When true the help overlay drops every
	// Dangerous binding from the rendered list and the per-page
	// hint strip is filtered through action.FilterDangerous before
	// being rendered. Page-level handlers also flash a hint instead
	// of dispatching the write — that gate is plumbed in at page
	// construction (each page's Options.ReadOnly).
	ReadOnly bool
	// HistoryDir is the resolved state directory for the prompt
	// history rings (`$XDG_STATE_HOME/a10r/` by default). Empty
	// disables persistence — each ring stays in-memory only, which
	// is what tests and headless flows want. The wiring layer
	// (cmd/tui.go) calls footer.DefaultHistoryDir to populate this
	// for the production binary.
	HistoryDir string
	// HintBar is the optional rotating tip strip (P2.W1.7). The
	// zero value is a disabled bar — no tick fires, the strip
	// renders empty so the footer collapses. Production turns it
	// on only when `tui.tips: true` is set in a10r.yaml; tests
	// pass the zero value so the tick scheduling stays out of the
	// fixture-driven Update loop.
	HintBar footer.HintBar
}

// App is the root bubbletea tea.Model. Pointer-receiver because it
// owns mutable subcomponent state (prompt, flash, page stack) that
// mutates across Update calls; bubbletea v2 accepts either value-
// or pointer-rooted Models, and pointer reads cleaner when the
// Dispatcher is itself pointer-typed.
type App struct {
	styles     *theme.Styles
	dispatcher *keys.Dispatcher
	cmdbar     *cmdbar.Resolver
	tenants    []string
	refresh    func(resource, scope string)
	readOnly   bool

	crumbs  footer.Crumbs
	prompt  footer.Prompt
	flash   footer.Flash
	hintbar footer.HintBar

	// histories backs the per-class recent-submissions rings
	// (P2.W1.8 / G4). Three classes — `:` always picks cmd, `/`
	// picks silence-matcher on the silences page and filter
	// elsewhere. nil rings are quiet no-ops, so a missing histories
	// entry simply disables cycling for that class.
	histories appHistories

	// stack is the page stack. Index 0 is the home page; the last
	// element is the active top-of-stack. Empty until the cmd
	// wiring (cmd/tui.go) pushes the first page.
	stack []Page

	// modal is the open async-result overlay (tenant picker, confirm
	// dialog, alert-page silence picker). When non-nil it captures
	// every key event before the dispatcher and renders in the body
	// slot. nil = no modal.
	modal modal.Modal

	// help is the open viewer overlay (the `?` keybindings catalogue).
	// When non-nil it captures every key event before the dispatcher
	// and renders in the body slot, exactly like modal does, but
	// without the async-result machinery — see ADR 0020. modal takes
	// precedence over help: `?` is dispatcher-gated and the dispatcher
	// is bypassed while a modal is open, so a pending decision is
	// never dismissed off-screen by a stray `?`.
	help *help.Help

	width  int
	height int

	quitting bool

	// timeFormat is the app-global toggle between relative
	// durations ("5m ago") and absolute ISO local timestamps. List
	// pages observe TimeFormatChangedMsg and re-render. Defaults
	// to relative — matches the pre-toggle UX every page shipped
	// with.
	timeFormat timerender.Format

	// pollCache stores the latest poll.DataMsg per
	// (ResourceLabel, Tenant) tuple. Updated as a side-effect of
	// the App's DataMsg interception in handleLifecycle, replayed
	// into a freshly-pushed page so the user sees populated rows
	// the moment the page lands rather than waiting up to a full
	// poll interval for the next tick. Pollers spawn at process
	// start (cmd/tui.go), so this cache is populated before the
	// user can navigate — every page push that comes after the
	// first round of fetches hydrates instantly.
	//
	// Outer key is the poll resource label ("alerts", "silences",
	// …); inner key is the tenant tag. Value is the full DataMsg
	// so At/NextAt come along for the page's footer countdown
	// without a separate ticker.
	//
	// Bounded: O(resources × tenants). The map shape is fixed at
	// 4 resources × N configured backends. Per-entry size is
	// dominated by the alert / silence payload it carries:
	//
	//   -  1 000 alerts × 4 resources ×  4 backends ~=  16 MiB resident
	//   -  5 000 alerts × 4 resources × 10 backends ~= 200 MiB resident
	//   - 10 000 alerts (storm) × 4 × 10            ~= 400 MiB resident
	//
	// Bounded ≠ small. Operators running long-lived sessions
	// against busy fleets should expect heap on the order of the
	// largest in-flight alert volume × number of backends. A
	// quit releases the cache fully; there is no leak.
	//
	// Single-threaded by construction: bubbletea routes every
	// Update through the same goroutine, so the App is the sole
	// reader and writer — no mutex needed.
	pollCache map[string]map[string]poll.DataMsg

	// statusCache stores the latest poll.BackendStatusMsg per tenant.
	// BackendStatusMsg is only emitted on state TRANSITIONS so a page
	// pushed AFTER the transition would otherwise never see the
	// failing-backend signal and silently render no error band.
	// Replayed on push the same way pollCache is, but unlike pollCache
	// the entries are pruned on a recovery transition (Detail empty)
	// so a backend that flapped and recovered before the page push
	// doesn't drag a stale error band onto the new page.
	//
	// Bounded: O(tenants). Single-threaded by construction, same
	// reasoning as pollCache.
	statusCache map[string]poll.BackendStatusMsg
}

// appHistories bundles the three per-class history rings the App
// hands to the prompt at Open time. Pulled out into a named struct
// so the picker (historyFor) reads as a small switch rather than a
// nested field path.
type appHistories struct {
	cmd            *footer.History
	filter         *footer.History
	silenceMatcher *footer.History
}

// newAppHistories loads (or creates lazily) the three rings under
// dir. An empty dir disables persistence — every ring stays
// in-memory, which is the test default. A non-empty dir is the
// production path; missing or malformed ring files degrade
// gracefully inside footer.NewHistory so this constructor is
// infallible.
func newAppHistories(dir string) appHistories {
	return appHistories{
		cmd:            footer.NewHistory(dir, footer.HistoryCmd),
		filter:         footer.NewHistory(dir, footer.HistoryFilter),
		silenceMatcher: footer.NewHistory(dir, footer.HistorySilenceMatcher),
	}
}

// historyFor picks the right ring for a (mode, top page label)
// pair. `:` always lands on cmd-history. `/` lands on
// silence-matcher-history when the silences page is on top —
// that page's filter walks Prom-style fields (creator / comment /
// matcher labels) and shouldn't share entries with the alerts
// substring filter — and on filter-history otherwise.
func (h appHistories) historyFor(mode footer.PromptMode, pageLabel string) *footer.History {
	if mode == footer.PromptCommand {
		return h.cmd
	}
	if pageLabel == "silences" {
		return h.silenceMatcher
	}
	return h.filter
}

// NewApp constructs an App with the supplied dependencies. Registers
// the always-on global bindings (Ctrl+C, q, ?, t, Esc, :, /, Ctrl+T)
// on the dispatcher's global layer so the app is usable before any
// page pushes its own. Page-specific layers are bound by the page's
// own Init.
func NewApp(opts Options) *App {
	resolver := opts.CmdBar
	if resolver == nil {
		resolver = cmdbar.New()
	}
	a := &App{
		styles:      opts.Styles,
		dispatcher:  opts.Dispatcher,
		cmdbar:      resolver,
		tenants:     opts.Tenants,
		refresh:     opts.Refresh,
		readOnly:    opts.ReadOnly,
		crumbs:      footer.NewCrumbs(),
		prompt:      footer.NewPrompt(resolver.Suggest),
		flash:       footer.NewFlash(),
		hintbar:     opts.HintBar,
		pollCache:   map[string]map[string]poll.DataMsg{},
		statusCache: map[string]poll.BackendStatusMsg{},
		histories:   newAppHistories(opts.HistoryDir),
	}
	a.registerGlobalBindings()
	a.registerTenantBindings()
	return a
}

// TimeFormat returns the app-global time-format toggle's current
// value. Page factories close over the App and read this at push
// time so a page opened *after* the user toggled `t` doesn't open
// in relative mode while the rest of the app reads absolute.
func (a *App) TimeFormat() timerender.Format { return a.timeFormat }

// Quitting reports whether the App has already authorised the quit
// (it set the flag when the QuitRequestedMsg cascade ran and
// emitted tea.Quit). The wiring layer's bubbletea filter consults
// this to distinguish "the App asked to quit cleanly" — let
// tea.QuitMsg through so the program stops — from "the runtime
// pushed a raw QuitMsg/InterruptMsg via SIGTERM/SIGINT" — translate
// to QuitRequestedMsg so the page-stack Close cascade runs first.
// Accessor (not a public field) so the bool stays immutable from
// outside the package: only handleLifecycle's QuitMsg branch flips it.
func (a *App) Quitting() bool { return a.quitting }

// Init implements tea.Model. Returns the hint-bar startup tick when
// the user opted in via `tui.tips: true`; nil otherwise so disabled
// runs schedule no work — the OFF-by-default short-circuit the
// project rule mandates.
func (a *App) Init() tea.Cmd { return a.hintbar.Start() }

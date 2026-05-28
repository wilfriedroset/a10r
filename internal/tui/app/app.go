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
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Options collects the dependencies the App needs to operate.
type Options struct {
	Styles     *theme.Styles
	Dispatcher *keys.Dispatcher
	// CmdBar resolves `:` command-bar aliases to tea.Cmds. Nil falls
	// back to an empty resolver — every `:command` flashes "unknown".
	CmdBar *cmdbar.Resolver
	// Tenants drives the top panel's `<0> all <1> name …` shortcut
	// column k9s-style.
	Tenants []string
	// Refresh is invoked on a RefreshRequestedMsg with the (resource,
	// scope) tuple. Nil falls back to a no-op.
	Refresh func(resource, scope string)
	// ReadOnly mirrors defaults.read_only / --read-only / A10R_READ_ONLY.
	// True drops Dangerous bindings from help, hints, and the
	// per-page dispatch; the gate is also threaded into each page's
	// Options.ReadOnly for page-local flash hints.
	ReadOnly bool
	// HistoryDir is `$XDG_STATE_HOME/a10r/` for production; empty
	// disables on-disk history (each ring stays in-memory).
	HistoryDir string
	// HintBar is the optional rotating tip strip; zero value is
	// disabled (no tick, empty render).
	HintBar footer.HintBar
}

// App is the root bubbletea tea.Model. Pointer-receiver because it
// owns mutable subcomponent state (prompt, flash, page stack) that
// changes across Update calls.
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

	// histories backs the per-class recent-submissions rings.
	// Three classes — `:` always picks cmd, `/`
	// picks silence-matcher on the silences page and filter
	// elsewhere. nil rings are quiet no-ops, so a missing histories
	// entry simply disables cycling for that class.
	histories appHistories

	// stack is the page stack. Index 0 is the home page; the last
	// element is the active top-of-stack. Empty until the cmd
	// wiring (cmd/tui.go) pushes the first page.
	stack []Page

	// overlays holds the two body-slot overlay surfaces. See the
	// overlays type below for the precedence + dispatcher discipline.
	overlays overlays

	width  int
	height int

	quitting bool

	// timeFormat is the app-global toggle between relative
	// durations ("5m ago") and absolute ISO local timestamps. List
	// pages observe TimeFormatChangedMsg and re-render. Defaults
	// to relative — matches the pre-toggle UX every page shipped
	// with.
	timeFormat timerender.Format

	// stateFormat is the app-global toggle between the full
	// (`9 active · 3 suppressed`) and compact (`9ac 3su`) renderings
	// of the alerts page's state breakdown. The alerts list and
	// group-detail instance list observe StateFormatChangedMsg and
	// re-render. Defaults to Full — the legible pre-toggle default.
	stateFormat stateformat.Format

	// caches holds the App's per-tenant poll-data and per-tenant
	// backend-status snapshots, replayed into a freshly-pushed page
	// so it shows populated rows immediately rather than waiting up
	// to a full poll interval for the next tick. See the caches type
	// below for the bounds and threading discipline.
	caches caches
}

// overlays bundles the two body-slot overlays the App can show in
// front of the active page. Both intercept every key event before
// the dispatcher and render in the body slot.
//
// modal is the async-result overlay (tenant picker, confirm dialog,
// alert-page silence picker). nil = no modal.
//
// help is the `?` keybindings catalogue (see ADR 0020). Same body-
// slot + key-capture shape as modal, without the async-result
// machinery. modal takes precedence: `?` is dispatcher-gated and the
// dispatcher is bypassed while a modal is open, so a pending decision
// is never dismissed off-screen by a stray `?`.
type overlays struct {
	modal modal.Modal
	help  *help.Help
}

// caches bundles the App's two replay snapshots. Both maps are
// updated as a side-effect of the App's DataMsg / BackendStatusMsg
// interception in handleLifecycle; pages pushed after the pollers
// have started hydrate from these instead of waiting on the next
// tick. Single-threaded by construction: bubbletea routes every
// Update through the same goroutine, so the App is the sole reader
// and writer — no mutex needed.
//
// poll: latest DataMsg per (ResourceLabel, Tenant). Outer key is
// the poll resource label ("alerts", "silences", …); inner key is
// the tenant tag. Value is the full DataMsg so At/NextAt come along
// for the page's footer countdown without a separate ticker.
// Bounded O(resources × tenants), but per-entry size scales with
// payload: 1 000 alerts × 4 resources × 4 backends ≈ 16 MiB,
// 5 000 × 4 × 10 ≈ 200 MiB, 10 000 (storm) × 4 × 10 ≈ 400 MiB.
// Bounded ≠ small; operators on busy fleets should expect heap on
// the order of the largest in-flight alert volume × backend count.
// Quit releases the maps fully; there is no leak.
//
// status: latest BackendStatusMsg per tenant. Transitions only —
// pages pushed AFTER a transition would otherwise miss the failing-
// backend signal. Entries are pruned on recovery (Detail empty) so
// a backend that flapped and recovered before push doesn't drag a
// stale error band onto the new page. Bounded O(tenants).
type caches struct {
	poll   map[string]map[string]poll.DataMsg
	status map[string]poll.BackendStatusMsg
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
		caches: caches{
			poll:   map[string]map[string]poll.DataMsg{},
			status: map[string]poll.BackendStatusMsg{},
		},
		histories: newAppHistories(opts.HistoryDir),
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

// StateFormat returns the app-global state-breakdown density's
// current value. Page factories close over the App and read this at
// push time so a group detail opened *after* the user toggled
// `Shift+T` opens in the same density the alerts list is showing.
func (a *App) StateFormat() stateformat.Format { return a.stateFormat }

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

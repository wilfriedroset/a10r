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
// Page interface, page-stack messages and TimeFormat live in
// page.go; modal slot messages in modal.go; key-name normalisation
// in keys.go.
package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Options collects the dependencies the App needs to operate.
// Exposed as a struct so the constructor stays additive: a future
// dependency (clock, browser, clipboard, logger) lands as a new
// field without touching every test that builds an App.
type Options struct {
	// Styles is the compiled theme. Re-rendered on every View call so
	// a future :theme command can hot-swap by replacing this field.
	Styles theme.Styles
	// Registry is the action registry. The app shell owns the global
	// layer's bindings; pages register their own when pushed (#23).
	Registry *action.Registry
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
}

// App is the root bubbletea tea.Model. Pointer-receiver because it
// owns mutable subcomponent state (prompt, flash, page stack) that
// mutates across Update calls; bubbletea v2 accepts either value-
// or pointer-rooted Models, and pointer reads cleaner when the
// Dispatcher and Registry are themselves pointer-typed.
type App struct {
	styles     theme.Styles
	registry   *action.Registry
	dispatcher *keys.Dispatcher
	cmdbar     *cmdbar.Resolver
	tenants    []string
	refresh    func(resource, scope string)

	crumbs footer.Crumbs
	prompt footer.Prompt
	flash  footer.Flash

	// stack is the page stack. Index 0 is the home page; the last
	// element is the active top-of-stack. Empty until the cmd
	// wiring (cmd/tui.go) pushes the first page.
	stack []Page

	// modal is the open overlay (tenant picker, confirm dialog).
	// When non-nil it captures every key event before the
	// dispatcher and renders in the body slot. nil = no modal.
	modal modal.Modal

	width  int
	height int

	quitting bool

	// timeFormat is the app-global toggle between relative
	// durations ("5m ago") and absolute ISO local timestamps. List
	// pages observe TimeFormatChangedMsg and re-render. Defaults
	// to relative — matches the pre-toggle UX every page shipped
	// with.
	timeFormat TimeFormat

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
	// without a separate ticker. Bounded: O(resources × tenants),
	// e.g. 4 × 4 = 16 entries in a typical multi-backend setup.
	//
	// Single-threaded by construction: bubbletea routes every
	// Update through the same goroutine, so the App is the sole
	// reader and writer — no mutex needed.
	pollCache map[string]map[string]poll.DataMsg
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
		styles:     opts.Styles,
		registry:   opts.Registry,
		dispatcher: opts.Dispatcher,
		cmdbar:     resolver,
		tenants:    opts.Tenants,
		refresh:    opts.Refresh,
		crumbs:     footer.NewCrumbs(),
		prompt:     footer.NewPrompt(resolver.Suggest),
		flash:      footer.NewFlash(),
		pollCache:  map[string]map[string]poll.DataMsg{},
	}
	a.registerGlobalBindings()
	a.registerTenantBindings()
	return a
}

// TimeFormat returns the app-global time-format toggle's current
// value. Page factories close over the App and read this at push
// time so a page opened *after* the user toggled `t` doesn't open
// in relative mode while the rest of the app reads absolute.
func (a *App) TimeFormat() TimeFormat { return a.timeFormat }

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return nil }

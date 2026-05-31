// SPDX-License-Identifier: Apache-2.0

// Package app assembles the bubbletea program for a10r: it owns the root
// tea.Model, frames the screen as header/body/footer, and routes messages
// between the dispatcher, header, body, and footer subcomponents.
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

const (
	scopeAll         = "all"
	keyNameEsc       = "Esc"
	keyDescDown      = "down"
	resourceSilences = "silences"
)

// Options collects the dependencies the App needs to operate.
type Options struct {
	Styles     *theme.Styles
	Dispatcher *keys.Dispatcher
	// CmdBar resolves `:` aliases to tea.Cmds. Nil falls back to an
	// empty resolver where every `:command` flashes "unknown".
	CmdBar *cmdbar.Resolver
	// Tenants drives the top panel's `<0> all <1> name …` column.
	Tenants []string
	// Refresh is invoked on a RefreshRequestedMsg. Nil is a no-op.
	Refresh func(resource, scope string)
	// ReadOnly drops Dangerous bindings from help, hints, and dispatch,
	// and is threaded into each page's Options.ReadOnly.
	ReadOnly bool
	// HistoryDir is `$XDG_STATE_HOME/a10r/`; empty keeps history in-memory.
	HistoryDir string
	// HintBar is the optional rotating tip strip; zero value is disabled.
	HintBar footer.HintBar
}

// App is the root bubbletea tea.Model. Pointer-receiver because it owns
// mutable subcomponent state that changes across Update calls.
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

	// histories backs the per-class recent-submissions rings. nil rings
	// are quiet no-ops, so a missing entry disables cycling for that class.
	histories appHistories

	// stack is the page stack: index 0 is home, the last element is the
	// active top-of-stack. Empty until cmd/tui.go pushes the first page.
	stack []Page

	// overlays holds the two body-slot overlay surfaces. See the overlays
	// type below for the precedence + dispatcher discipline.
	overlays overlays

	width  int
	height int

	quitting bool

	// timeFormat toggles relative vs absolute timestamps app-wide.
	// Defaults to relative to match the pre-toggle UX.
	timeFormat timerender.Format

	// stateFormat toggles full vs compact state-breakdown rendering
	// app-wide. Defaults to Full, the legible pre-toggle default.
	stateFormat stateformat.Format

	// caches holds poll-data and backend-status snapshots, replayed into a
	// freshly-pushed page so it shows rows without waiting for the next
	// tick. See the caches type below for bounds and threading discipline.
	caches caches
}

// overlays bundles the two body-slot overlays (modal, help; see ADR 0020),
// both intercepting keys before the dispatcher. modal takes precedence:
// `?` is dispatcher-gated and the dispatcher is bypassed while a modal is
// open, so a pending decision is never dismissed by a stray `?`.
type overlays struct {
	modal modal.Modal
	help  *help.Help
}

// caches bundles the App's two replay snapshots, written as a side-effect
// of handleLifecycle's DataMsg/BackendStatusMsg interception. Single-
// threaded by construction: bubbletea routes every Update through one
// goroutine, so the App is sole reader/writer and no mutex is needed.
//
// poll: latest DataMsg per (ResourceLabel, Tenant). Bounded O(resources ×
// tenants), but per-entry size scales with payload — a 10 000-alert storm
// × 4 resources × 10 backends is ~400 MiB heap. Bounded ≠ small.
//
// status: latest BackendStatusMsg per tenant, pruned on recovery (empty
// Detail) so a backend that flapped and recovered before push doesn't drag
// a stale error band onto the new page.
type caches struct {
	poll   map[string]map[string]poll.DataMsg
	status map[string]poll.BackendStatusMsg
}

// appHistories bundles the three per-class history rings the App hands
// to the prompt at Open time.
type appHistories struct {
	cmd            *footer.History
	filter         *footer.History
	silenceMatcher *footer.History
}

// newAppHistories loads the three rings under dir; an empty dir keeps
// them in-memory. Infallible: footer.NewHistory degrades gracefully on
// missing or malformed ring files.
func newAppHistories(dir string) appHistories {
	return appHistories{
		cmd:            footer.NewHistory(dir, footer.HistoryCmd),
		filter:         footer.NewHistory(dir, footer.HistoryFilter),
		silenceMatcher: footer.NewHistory(dir, footer.HistorySilenceMatcher),
	}
}

// historyFor picks the ring for a (mode, top page label) pair. `/` on the
// silences page uses the silence-matcher ring (its Prom-field filter
// shouldn't share entries with the alerts substring filter), else filter.
func (h appHistories) historyFor(mode footer.PromptMode, pageLabel string) *footer.History {
	if mode == footer.PromptCommand {
		return h.cmd
	}
	if pageLabel == resourceSilences {
		return h.silenceMatcher
	}
	return h.filter
}

// NewApp constructs an App and registers the always-on global bindings
// so the app is usable before any page pushes its own.
func NewApp(opts Options) *App {
	resolver := opts.CmdBar
	if resolver == nil {
		resolver = cmdbar.New()
	}
	a := &App{
		styles:     opts.Styles,
		dispatcher: opts.Dispatcher,
		cmdbar:     resolver,
		tenants:    opts.Tenants,
		refresh:    opts.Refresh,
		readOnly:   opts.ReadOnly,
		crumbs:     footer.NewCrumbs(),
		prompt:     footer.NewPrompt(resolver.Suggest),
		flash:      footer.NewFlash(),
		hintbar:    opts.HintBar,
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

// TimeFormat returns the app-global time-format value; page factories read
// it at push time so a page opened after a `t` toggle stays consistent.
func (a *App) TimeFormat() timerender.Format { return a.timeFormat }

// StateFormat returns the app-global density value; page factories read it
// at push time so a page opened after a `Shift+T` toggle stays consistent.
func (a *App) StateFormat() stateformat.Format { return a.stateFormat }

// Quitting reports whether the App authorised a clean quit. The wiring
// layer's bubbletea filter consults it to let an App-driven tea.QuitMsg
// through, versus rewriting a raw SIGTERM/SIGINT QuitMsg into
// QuitRequestedMsg so the page-stack Close cascade runs first.
func (a *App) Quitting() bool { return a.quitting }

// Init implements tea.Model. Returns the hint-bar startup tick only when
// tips are enabled, so disabled runs schedule no work.
func (a *App) Init() tea.Cmd { return a.hintbar.Start() }

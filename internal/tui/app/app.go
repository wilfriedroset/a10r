// SPDX-License-Identifier: Apache-2.0

// Package app assembles the bubbletea program for a10r: it owns the root
// tea.Model, frames the screen as header/body/footer, and routes messages
// between the dispatcher, header, body, and footer subcomponents.
package app

import (
	"log/slog"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/notify"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/session"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

const (
	scopeAll         = "all"
	keyNameEsc       = "Esc"
	keyDescDown      = "down"
	resourceSilences = "silences"
	resourceAlerts   = "alerts"
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
	// Session is the live configuration. The App reads the read-only
	// switch from it for the chrome it composes on demand: the help
	// overlay on every `?` and the window title on every frame. Must
	// not be nil.
	Session *session.Session
	// HistoryDir is `$XDG_STATE_HOME/a10r/`; empty keeps history in-memory.
	HistoryDir string
	// HintBar is the optional rotating tip strip; zero value is disabled.
	HintBar footer.HintBar
	// Scope is the tenant scope the app boots with: "all", one backend
	// name, or a comma-joined subset. Empty reads as "all".
	Scope string
	// SaveScope is handed every scope the user switches to, so the
	// next run can open on it. Nil is a no-op.
	SaveScope func(scope string)
	// AutoTheme opts into terminal-background detection: Styles holds
	// the provisional dark skin and the App swaps it for the light one
	// when the terminal reports a light background. Off means the user
	// named a skin, and that choice is never second-guessed.
	AutoTheme bool
	// LoadStyles compiles a skin by name for the auto-theme swap and
	// for `:skin`. Nil disables both, which leaves the provisional
	// skin in place.
	LoadStyles func(name string) (*theme.Styles, error)
	// SkinNames lists every skin `:skin` can offer and apply. Nil
	// disables `:skin`, which then says so rather than failing quietly.
	SkinNames func() []string
	// SkinName is the skin the app starts on, which the picker marks
	// as current. Under auto-detection it is the provisional one until
	// the terminal answers.
	SkinName string
	// Reload re-reads the configuration and applies the reloadable
	// subset, backing `:reload`. Nil disables the verb, which then
	// says so rather than looking like a reload that changed nothing.
	// The App refuses the request behind an open form before it calls
	// this; everything else about the reload belongs to the caller.
	Reload func() tea.Cmd
	// Notify rings and raises a desktop notification on a new firing
	// alert. A reload hands it the session's tui.notify. Nil builds a
	// disabled one.
	Notify *notify.Notifier
}

// App is the root bubbletea tea.Model. Pointer-receiver because it owns
// mutable subcomponent state that changes across Update calls.
type App struct {
	styles     *theme.Styles
	dispatcher *keys.Dispatcher
	cmdbar     *cmdbar.Resolver
	tenants    []string
	refresh    func(resource, scope string)
	session    *session.Session

	// scope mirrors the active tenant scope so the window title can name
	// it. Pages own their own copy; the App keeps one because the title
	// outlives any single page.
	scope     string
	saveScope func(scope string)

	// notify watches the alert polls for a newly firing aggregate.
	notify *notify.Notifier

	// autoTheme is armed at boot and disarmed by the first background
	// colour report, so detection runs once per process.
	autoTheme  bool
	loadStyles func(name string) (*theme.Styles, error)

	// skinNames and skinName back `:skin`. skinName is the applied
	// skin, kept so the picker can mark it and so auto-detection and
	// `:skin` agree on what is in force.
	skinNames func() []string
	skinName  string
	// configSkin is the theme.name of the config last applied, so a
	// reload can tell a file edit from a `:skin` pick it must keep.
	configSkin string

	// reload backs `:reload`; see Options.Reload.
	reload func() tea.Cmd

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
		session:    session.Must(opts.Session),
		scope:      opts.Scope,
		saveScope:  opts.SaveScope,
		notify:     opts.Notify,

		autoTheme:  opts.AutoTheme && opts.LoadStyles != nil,
		loadStyles: opts.LoadStyles,
		skinNames:  opts.SkinNames,
		skinName:   opts.SkinName,
		reload:     opts.Reload,

		crumbs:  footer.NewCrumbs(),
		prompt:  footer.NewPrompt(resolver.Suggest),
		flash:   footer.NewFlash(),
		hintbar: opts.HintBar,
		caches: caches{
			poll:   map[string]map[string]poll.DataMsg{},
			status: map[string]poll.BackendStatusMsg{},
		},
		histories: newAppHistories(opts.HistoryDir),
	}
	a.configSkin = a.session.Config().Theme.Name
	if a.notify == nil {
		a.notify = notify.New(config.Notify{})
	}
	// The boot scope has to reach the notifier here: a run that starts
	// narrowed would otherwise announce every tenant until the user
	// changes the scope.
	a.notify.SetScope(opts.Scope)
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

// SkinName returns the skin in force, which `:skin` and auto-detection
// move without touching the config.
func (a *App) SkinName() string { return a.skinName }

// Quitting reports whether the App authorised a clean quit. The wiring
// layer's bubbletea filter consults it to let an App-driven tea.QuitMsg
// through, versus rewriting a raw SIGTERM/SIGINT QuitMsg into
// QuitRequestedMsg so the page-stack Close cascade runs first.
func (a *App) Quitting() bool { return a.quitting }

// Init implements tea.Model. Returns the hint-bar startup tick only when
// tips are enabled, so disabled runs schedule no work, batched with the
// terminal background query when the skin is left to auto-detection.
//
// The first frame renders with the provisional skin either way: the
// query is asynchronous, and a terminal that never answers simply keeps
// that skin.
func (a *App) Init() tea.Cmd {
	if !a.autoTheme {
		return a.hintbar.Start()
	}
	return tea.Batch(a.hintbar.Start(), tea.RequestBackgroundColor)
}

// applyAutoTheme resolves the auto sentinel against the reported
// terminal background and swaps the skin in place.
//
// The write is through the shared *theme.Styles pointer every page and
// chrome component was constructed with, so one assignment restyles the
// whole tree without touching the page stack. Safe because bubbletea
// calls View inline after Update on the one event-loop goroutine, so
// no frame can read the struct mid-assignment.
func (a *App) applyAutoTheme(dark bool) {
	if !a.autoTheme {
		return
	}
	a.autoTheme = false

	styles, err := a.loadStyles(theme.AutoSkinFor(dark))
	if err != nil {
		slog.Warn("auto theme detection failed; keeping the startup skin",
			slog.Bool("dark", dark),
			slog.Any("error", err),
		)
		return
	}
	*a.styles = *styles
	a.skinName = theme.AutoSkinFor(dark)
}

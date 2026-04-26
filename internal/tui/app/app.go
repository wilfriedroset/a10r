// SPDX-License-Identifier: Apache-2.0

// Package app assembles the bubbletea program for a10r: it owns
// the root tea.Model, frames the screen as header/body/footer, and
// routes messages between the dispatcher, header, body, and footer
// subcomponents. v0.1 of this package ships the frame plus the
// global keybindings (Ctrl+C, q, ?); the page stack lands in #23,
// the polling lifecycle in #24, the modal+picker in #25, the
// command bar in #26, and the first real page (alerts) in #27.
package app

import (
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/panel"
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
}

// App is the root bubbletea tea.Model. Pointer-receiver because it
// owns mutable subcomponent state (prompt, flash, page stack in
// #23) that mutates across Update calls; bubbletea v2 accepts
// either value- or pointer-rooted Models, and pointer reads cleaner
// when the Dispatcher and Registry are themselves pointer-typed.
type App struct {
	styles     theme.Styles
	registry   *action.Registry
	dispatcher *keys.Dispatcher
	cmdbar     *cmdbar.Resolver
	tenants    []string

	crumbs footer.Crumbs
	prompt footer.Prompt
	flash  footer.Flash

	// stack is the page stack. Index 0 is the home page; the last
	// element is the active top-of-stack. Empty until the cmd
	// wiring (#27 / cmd/tui.go) pushes the first page.
	stack []Page

	// modal is the open overlay (tenant picker, confirm dialog).
	// When non-nil it captures every key event before the
	// dispatcher and renders in the body slot. nil = no modal.
	modal modal.Modal

	width  int
	height int

	quitting bool
}

// NewApp constructs an App with the supplied dependencies. Registers
// the always-on global bindings (Ctrl+C, q, ?) on the dispatcher's
// global layer so the app is usable before any page pushes its own.
// Page-specific layers and the table-context layer remain unbound
// until #23 / #27 wire them in.
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
		crumbs:     footer.NewCrumbs(),
		prompt:     footer.NewPrompt(),
		flash:      footer.NewFlash(),
	}
	a.registerGlobalBindings()
	a.registerTenantBindings()
	return a
}

// registerTenantBindings wires the numeric quick-switch keys
// (`0` for all-tenants, `1`-`9` for the Nth configured backend)
// at LayerGlobal. Pressing one emits a ScopeChangedMsg the top
// page consumes to filter its view and update its title.
func (a *App) registerTenantBindings() {
	a.dispatcher.Set(keys.LayerGlobal, "0", func() tea.Cmd {
		return func() tea.Msg { return ScopeChangedMsg{Scope: "all"} }
	})
	for i, name := range a.tenants {
		if i >= 9 {
			break // numeric quick-switch tops out at 1-9 per C3
		}
		captured := name
		a.dispatcher.Set(keys.LayerGlobal, strconv.Itoa(i+1), func() tea.Cmd {
			return func() tea.Msg { return ScopeChangedMsg{Scope: captured} }
		})
	}
}

// registerGlobalBindings wires the keybindings.md §Global entries
// the app shell owns directly. Tenant quick-switch (#35) ships
// with its own subsystem so it can be unit-tested in isolation.
func (a *App) registerGlobalBindings() {
	a.dispatcher.Set(keys.LayerGlobal, "Ctrl+C", func() tea.Cmd { return tea.Quit })
	a.dispatcher.Set(keys.LayerGlobal, "q", func() tea.Cmd { return tea.Quit })
	// `Esc` falls through to "pop stack" at the global layer per
	// keybindings.md. Modal / prompt layers shadow this when active
	// so Esc dismisses them first.
	a.dispatcher.Set(keys.LayerGlobal, "Esc", PopPage)
	// `:` opens the command bar; `/` opens the filter prompt. The
	// dispatcher only fires the open; the resulting PromptSubmitted
	// / PromptCancelled messages are handled by handleInput later.
	a.dispatcher.Set(keys.LayerGlobal, ":", a.openPromptCmd(footer.PromptCommand))
	a.dispatcher.Set(keys.LayerGlobal, "/", a.openPromptCmd(footer.PromptFilter))
	// `?` opens the k9s-style help overlay. The bindings are
	// composed at open-time so the RESOURCE column always reflects
	// whichever page is on top of the stack. Globals and table
	// motions are curated lists kept here (rather than re-derived
	// from the dispatcher, which stores handlers, not descriptions).
	a.dispatcher.Set(keys.LayerGlobal, "?", func() tea.Cmd {
		return OpenModal(func() modal.Modal {
			return help.New(help.Options{
				PageName:     a.activeViewLabel(),
				PageBindings: a.activePageBindings(),
				Globals:      globalsCatalog(),
				TableMotions: tableMotionsCatalog(),
				Tenants:      a.tenants,
				Styles:       a.styles,
			})
		})
	})
}

// globalsCatalog is the GENERAL-column list rendered in the help
// overlay. Source-of-truth pairs with `keybindings.md §Global` so
// any binding the dispatcher gains a registration for above shows
// up here too.
func globalsCatalog() []action.Action {
	return []action.Action{
		{Key: ":", Description: "command"},
		{Key: "/", Description: "filter"},
		{Key: "?", Description: "help"},
		{Key: "r", Description: "refresh"},
		{Key: "Esc", Description: "back"},
		{Key: "q", Description: "quit"},
		{Key: "Ctrl+C", Description: "force quit"},
		{Key: "Ctrl+T", Description: "tenant picker"},
	}
}

// tableMotionsCatalog is the NAVIGATION-column list. Mirrors the
// table-context block from `keybindings.md` so the help overlay
// reads the same affordances the dispatcher serves.
func tableMotionsCatalog() []action.Action {
	return []action.Action{
		{Key: "j", Description: "down"},
		{Key: "k", Description: "up"},
		{Key: "h", Description: "prev column"},
		{Key: "l", Description: "next column"},
		{Key: "gg", Description: "top"},
		{Key: "G", Description: "bottom"},
		{Key: "Ctrl+D", Description: "page down"},
		{Key: "Ctrl+U", Description: "page up"},
		{Key: "Enter", Description: "drill"},
		{Key: "Space", Description: "mark"},
	}
}

// activePageBindings returns the top-of-stack page's Bindings(),
// or nil when no page is pushed (the empty-app placeholder body).
func (a *App) activePageBindings() []action.Action {
	if p := a.topPage(); p != nil {
		return p.Bindings()
	}
	return nil
}

// openPromptCmd returns a Handler that opens the bottom-strip
// prompt in the given mode. State mutation runs synchronously when
// the dispatcher fires; for filter mode, an Opened message
// reaches the top page so it can snapshot pre-filter state per
// PromptOpenedMsg's contract.
func (a *App) openPromptCmd(mode footer.PromptMode) func() tea.Cmd {
	return func() tea.Cmd {
		a.prompt = a.prompt.Open(mode)
		if mode == footer.PromptFilter {
			return func() tea.Msg { return footer.PromptOpenedMsg{Mode: mode} }
		}
		return nil
	}
}

// handlePromptSubmitted routes a prompt's submission. Command
// values go through cmdbar.Resolve; unknown / ambiguous errors
// surface as Warn flashes; empty input is silent so the user can
// back out of an open `:` prompt by pressing Enter without typing.
// Filter values flow through to the top page, which decides what a
// filter string means in its own context.
func (a *App) handlePromptSubmitted(m footer.PromptSubmittedMsg) tea.Cmd {
	if m.Mode == footer.PromptFilter {
		return a.forwardToTop(m)
	}
	cmd, err := a.cmdbar.Resolve(m.Value)
	if err == nil {
		return cmd
	}
	if errors.Is(err, cmdbar.ErrEmpty) {
		return nil
	}
	return showFlash(footer.FlashWarn, err.Error())
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return nil }

// Update implements tea.Model. Routes the message to whichever
// subcomponent owns it; falls back to the dispatcher for plain key
// events. Returns the model verbatim and the resulting command.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, handled := a.handleLifecycle(msg); handled {
		return a, cmd
	}
	if cmd, handled := a.handleInput(msg); handled {
		return a, cmd
	}
	if isModalResult(msg) {
		a.closeModal()
		cmd := a.forwardToTop(msg)
		return a, cmd
	}
	if a.flash.Owns(msg) {
		var cmd tea.Cmd
		a.flash, cmd = a.flash.Update(msg)
		return a, cmd
	}
	cmd := a.forwardToTop(msg)
	return a, cmd
}

// handleLifecycle covers the App's own message types: window
// resize, quit, chord-timer, page stack ops, modal slot ops.
// Returns (cmd, true) when handled.
func (a *App) handleLifecycle(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a.forwardToTop(m), true
	case tea.QuitMsg:
		a.quitting = true
		return nil, true
	case keys.ChordExpiredMsg:
		return a.dispatcher.HandleChordExpired(m), true
	case pushPageMsg:
		return a.pushPage(m.Factory), true
	case popPageMsg:
		return a.popPage(), true
	case replacePageMsg:
		return a.replacePage(m.Factory), true
	case openModalMsg:
		return a.openModal(m.Factory), true
	case closeModalMsg:
		a.closeModal()
		return nil, true
	}
	return nil, false
}

// handleInput covers the input pipeline: prompt results, paste,
// key-release / paste-framing no-ops, and key presses. Returns
// (cmd, true) when handled.
func (a *App) handleInput(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case footer.PromptSubmittedMsg:
		return a.handlePromptSubmitted(m), true
	case footer.PromptCancelledMsg:
		// Filter cancellations flow through to the top page so a
		// page that snapshotted its filter state on prompt-open can
		// roll back. Command cancellations terminate at the App;
		// no observable state change.
		if m.Mode == footer.PromptFilter {
			return a.forwardToTop(m), true
		}
		return nil, true
	case tea.PasteMsg:
		if a.prompt.IsOpen() {
			var cmd tea.Cmd
			a.prompt, cmd = a.prompt.Update(m)
			return cmd, true
		}
		return a.forwardToTop(m), true
	case tea.KeyReleaseMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		// Bubbletea v2 emits release events alongside key presses
		// when key-release reporting is enabled, plus paste-start /
		// paste-end framing around bracketed paste. The app shell
		// has no use for either today; explicit no-ops keep them out
		// of the flash catch-all so the routing intent is auditable.
		return nil, true
	case tea.KeyPressMsg:
		_, cmd := a.handleKey(m)
		return cmd, true
	}
	return nil, false
}

// forwardToTop delivers msg to the top-of-stack page (if any). The
// page is value-typed in the stack so the new derivative replaces
// the slot in-place. Returns the page's Cmd or nil when the stack
// is empty.
func (a *App) forwardToTop(msg tea.Msg) tea.Cmd {
	if len(a.stack) == 0 {
		return nil
	}
	top, cmd := a.stack[len(a.stack)-1].Update(msg)
	a.stack[len(a.stack)-1] = top
	a.refreshCrumbs()
	return cmd
}

// pushPage adds a new page on top, runs its Init, and refreshes the
// crumb strip. Returns the page's Init Cmd so callers can chain
// follow-ups (e.g. an alerts page kicking off a poll).
func (a *App) pushPage(factory func() Page) tea.Cmd {
	if factory == nil {
		return nil
	}
	page := factory()
	if page == nil {
		return nil
	}
	a.stack = append(a.stack, page)
	a.refreshCrumbs()
	return page.Init()
}

// popPage removes the top page when the stack has more than one
// entry, calling Close on it so pollers and other background work
// can wind down. Popping the last page is a no-op so the home view
// always stays visible — a keybindings.md "Esc on home view does
// nothing" is friendlier than ejecting the user into a black screen.
func (a *App) popPage() tea.Cmd {
	if len(a.stack) <= 1 {
		return nil
	}
	departing := a.stack[len(a.stack)-1]
	a.stack = a.stack[:len(a.stack)-1]
	a.refreshCrumbs()
	return departing.Close()
}

// replacePage swaps the top page for the factory's output. The
// displaced page's Close runs so its background work tears down
// before the new page's Init starts; the two Cmds run sequentially
// to keep that ordering deterministic. When the stack is empty,
// replacePage falls back to push so a user can always launch a
// fresh view from a no-page state.
func (a *App) replacePage(factory func() Page) tea.Cmd {
	if factory == nil {
		return nil
	}
	if len(a.stack) == 0 {
		return a.pushPage(factory)
	}
	page := factory()
	if page == nil {
		return nil
	}
	departing := a.stack[len(a.stack)-1]
	a.stack[len(a.stack)-1] = page
	a.refreshCrumbs()
	return tea.Sequence(departing.Close(), page.Init())
}

// refreshCrumbs rebuilds the breadcrumb strip from the current
// stack. Cheap on every frame because Crumbs.Set already does a
// defensive copy.
func (a *App) refreshCrumbs() {
	labels := make([]string, len(a.stack))
	for i, p := range a.stack {
		labels[i] = p.Crumb()
	}
	a.crumbs = a.crumbs.Set(labels)
}

// topPage returns the active page, or nil when the stack is empty.
func (a *App) topPage() Page {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

// activeViewLabel returns the top page's crumb (used by the help
// overlay to label its per-view section). Empty when no page is
// pushed.
func (a *App) activeViewLabel() string {
	if p := a.topPage(); p != nil {
		return p.Crumb()
	}
	return ""
}

// handleKey routes a single key event. Precedence:
//
//  1. Open modal — captures every key including Esc, per
//     keybindings.md. Esc inside the modal closes the modal.
//  2. Open prompt — same rule for the bottom-strip prompt.
//  3. Dispatcher precedence stack (modal > prompt > view > table
//     > global). Bindings live at whichever layer makes sense.
//  4. Top page — a final catch-all so vim motions and custom
//     shortcuts a page handles locally don't need pre-registration.
//
// Unconsumed keys drop silently because most keys (j/k, shifted
// letters, etc.) are valid no-ops on placeholder pages.
func (a *App) handleKey(m tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if a.modal != nil {
		next, cmd := a.modal.Update(m)
		a.modal = next
		return a, cmd
	}
	if a.prompt.IsOpen() {
		var cmd tea.Cmd
		a.prompt, cmd = a.prompt.Update(m)
		return a, cmd
	}

	keyName := normalizeKey(m)
	if keyName == "" {
		return a, nil
	}
	consumed, cmd := a.dispatcher.Dispatch(keyName)
	if consumed {
		return a, cmd
	}
	// Unbound at the dispatcher: forward to the top page so it can
	// react (vim motions, custom shortcuts) without the app shell
	// pre-knowing every page's binding set.
	cmd = a.forwardToTop(m)
	return a, cmd
}

// View implements tea.Model. Composes the k9s-style top panel,
// a bordered body containing either the open modal or the top
// page's view, and the footer (crumbs + prompt + flash) into a
// full-screen alt-screen view.
func (a *App) View() tea.View {
	if a.width == 0 || a.height == 0 {
		// Pre-resize: bubbletea's first WindowSizeMsg arrives in
		// the next loop iteration. Render an empty alt-screen
		// view so we don't crash with a zero-width Render.
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}

	topPanel := panel.RenderTop(a.panelState(), a.styles)
	footerLines := a.renderFooter()

	bodyHeight := max(a.height-linesIn(topPanel)-linesIn(footerLines), 0)
	body := a.renderBody(bodyHeight)

	out := lipgloss.JoinVertical(lipgloss.Left, topPanel, body, footerLines)
	v := tea.NewView(out)
	v.AltScreen = true
	return v
}

// panelState builds the top-panel state from the app shell's
// view of the world plus the active page's metadata. Info column
// shows tenant + version; tenants column shows the numeric
// shortcuts; hints column comes from the top page's Bindings;
// logo is the package-level constant.
func (a *App) panelState() panel.State {
	state := panel.State{
		Width:   a.width,
		Logo:    panel.Logo,
		Tenants: tenantBindings(a.tenants),
		Info: []panel.InfoLine{
			{Label: "tenants", Value: "—"},
			{Label: "alerts", Value: "—"},
			{Label: "version", Value: "v0.1.0"},
		},
	}
	if p := a.topPage(); p != nil {
		state.Hints = p.Bindings()
	}
	return state
}

// tenantBindings produces the numeric shortcut catalogue for the
// panel: <0> all + <1>..<9> for the first nine configured
// tenants. Empty input still emits <0> all so the column is
// present-but-trivial in zero-backend / wizard runs.
func tenantBindings(tenants []string) []panel.TenantBinding {
	out := make([]panel.TenantBinding, 0, len(tenants)+1)
	out = append(out, panel.TenantBinding{Key: "0", Name: "all"})
	for i, t := range tenants {
		if i >= 9 {
			break // numeric quick-switch tops out at 1-9 per C3
		}
		out = append(out, panel.TenantBinding{Key: strconv.Itoa(i + 1), Name: t})
	}
	return out
}

// renderBody fills the bordered-body slot. Modal wins when one
// is open; the top page draws its View otherwise; an empty stack
// renders a styled blank pane. When the active page has a non-
// empty HeaderContent (filter / sort / mark indicators), it
// renders as a subtitle line directly below the title border so
// the user can spot the active shaping at a glance.
func (a *App) renderBody(height int) string {
	innerHeight := max(height-2, 0) // -2 for top + bottom borders
	innerWidth := max(a.width-2, 0)

	var inner, title string
	switch {
	case a.modal != nil:
		title = a.modal.Title()
		if title == "" {
			title = "modal"
		}
		inner = a.modal.View(innerWidth, innerHeight)
	case a.topPage() != nil:
		p := a.topPage()
		title = p.Title()
		if title == "" {
			title = p.Crumb()
		}
		subtitle := p.HeaderContent()
		if subtitle != "" {
			inner = subtitle + "\n" + p.View(innerWidth, max(innerHeight-1, 0))
		} else {
			inner = p.View(innerWidth, innerHeight)
		}
	default:
		title = "a10r"
		inner = ""
	}
	return panel.RenderBody(a.width, height, inner, title, a.styles)
}

// renderFooter stacks the crumbs / prompt / flash strips. Each can
// be empty; the join collapses empty rows so the body fills the
// freed space.
func (a *App) renderFooter() string {
	parts := make([]string, 0, 3)
	if s := a.crumbs.Render(a.styles); s != "" {
		parts = append(parts, s)
	}
	if s := a.prompt.Render(a.styles); s != "" {
		parts = append(parts, s)
	}
	if s := a.flash.Render(a.styles); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// linesIn returns the number of "\n"-separated rows in s. Empty
// string is zero rows (collapses) so callers can treat an empty
// strip the same as no strip at all.
func linesIn(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// showFlash returns a tea.Cmd that emits a FlashShowMsg with the
// given level and text. Used by the global bindings the app shell
// owns so they can drop a hint without holding a Flash reference.
func showFlash(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

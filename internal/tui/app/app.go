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
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
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

	crumbs footer.Crumbs
	prompt footer.Prompt
	flash  footer.Flash

	// stack is the page stack. Index 0 is the home page; the last
	// element is the active top-of-stack. Empty until the cmd
	// wiring (#27 / cmd/tui.go) pushes the first page.
	stack []Page

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
	a := &App{
		styles:     opts.Styles,
		registry:   opts.Registry,
		dispatcher: opts.Dispatcher,
		crumbs:     footer.NewCrumbs(),
		prompt:     footer.NewPrompt(),
		flash:      footer.NewFlash(),
	}
	a.registerGlobalBindings()
	return a
}

// registerGlobalBindings wires the keybindings.md §Global entries
// the app shell owns directly. Other globals (`:`, `/`, `Ctrl+T`,
// numeric tenant quick-switch) ship with their respective subsystems
// so each can be unit-tested in isolation.
func (a *App) registerGlobalBindings() {
	a.dispatcher.Set(keys.LayerGlobal, "Ctrl+C", func() tea.Cmd { return tea.Quit })
	a.dispatcher.Set(keys.LayerGlobal, "q", func() tea.Cmd { return tea.Quit })
	// `Esc` falls through to "pop stack" at the global layer per
	// keybindings.md. Modal / prompt layers shadow this when active
	// so Esc dismisses them first.
	a.dispatcher.Set(keys.LayerGlobal, "Esc", PopPage)
	// `?` reaches the help overlay in #37; today it's a placeholder
	// that flashes a friendly message so users discover the binding.
	a.dispatcher.Set(keys.LayerGlobal, "?", func() tea.Cmd {
		return showFlash(footer.FlashInfo, "help overlay arrives in #37")
	})
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return nil }

// Update implements tea.Model. Routes the message to whichever
// subcomponent owns it; falls back to the dispatcher for plain key
// events. Returns the model verbatim and the resulting command.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		cmd := a.forwardToTop(m)
		return a, cmd
	case tea.QuitMsg:
		a.quitting = true
		return a, nil
	case keys.ChordExpiredMsg:
		cmd := a.dispatcher.HandleChordExpired(m)
		return a, cmd
	case pushPageMsg:
		cmd := a.pushPage(m.Factory)
		return a, cmd
	case popPageMsg:
		cmd := a.popPage()
		return a, cmd
	case replacePageMsg:
		cmd := a.replacePage(m.Factory)
		return a, cmd
	case footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		// Routing of the resolved value is wired in #26. For now we
		// only acknowledge so the prompt's Cmd doesn't bubble up to
		// bubbletea's default handler (which would log a warning).
		return a, nil
	case tea.PasteMsg:
		if a.prompt.IsOpen() {
			var cmd tea.Cmd
			a.prompt, cmd = a.prompt.Update(m)
			return a, cmd
		}
		cmd := a.forwardToTop(m)
		return a, cmd
	case tea.KeyReleaseMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		// Bubbletea v2 emits release events alongside key presses
		// when key-release reporting is enabled, plus paste-start /
		// paste-end framing around bracketed paste. The app shell
		// has no use for either today; explicit no-ops keep them out
		// of the flash catch-all so the routing intent is auditable.
		return a, nil
	case tea.KeyPressMsg:
		return a.handleKey(m)
	}
	// Flash-domain messages (the public FlashShowMsg and the
	// internal auto-clear tick) route to flash exclusively. Everything
	// else is page-domain so it goes to the top page only. This
	// avoids the "every component must no-op on foreign types"
	// invariant that a blanket forward would impose, which would
	// scale badly once #24 lands data ticks.
	if a.flash.Owns(msg) {
		var cmd tea.Cmd
		a.flash, cmd = a.flash.Update(msg)
		return a, cmd
	}
	cmd := a.forwardToTop(msg)
	return a, cmd
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

// handleKey routes a single key event. An open prompt captures
// every key including Esc — Esc dismisses the prompt itself per
// keybindings.md ("Esc always reaches the modal/prompt to dismiss
// it"). Otherwise the dispatcher's precedence stack decides.
// Unconsumed keys drop silently because most keys (j/k motion,
// shifted letters, etc.) are bound at the page layer and are valid
// no-ops on placeholder pages.
func (a *App) handleKey(m tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

// View implements tea.Model. Composes header (top), body (the top
// page's view, or a placeholder when the stack is empty), and
// footer (bottom) into a full-screen alt-screen view. Subcomponents
// render through theme.Styles so a theme swap re-paints without
// touching this code.
func (a *App) View() tea.View {
	if a.width == 0 || a.height == 0 {
		// Pre-resize: bubbletea's first WindowSizeMsg arrives in the
		// next loop iteration. Render an empty alt-screen view so we
		// don't crash with a zero-width Render.
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}

	headerLine := header.Render(a.headerState(), a.styles)
	footerLines := a.renderFooter()

	bodyHeight := max(a.height-1-linesIn(footerLines), 0)
	body := a.renderBody(bodyHeight)

	out := lipgloss.JoinVertical(lipgloss.Left, headerLine, body, footerLines)
	v := tea.NewView(out)
	v.AltScreen = true
	return v
}

// headerState builds the header.State from the app shell's view of
// the world plus the top page's per-view contributions. Connection
// state, count, age, and tenant label remain placeholder until the
// polling lifecycle (#24) and tenant management (#35) populate them.
func (a *App) headerState() header.State {
	state := header.State{Width: a.width}
	if p := a.topPage(); p != nil {
		state.Content = p.HeaderContent()
		state.Hints = p.Bindings()
	}
	return state
}

// renderBody asks the top page to draw its body, or emits a styled
// blank pane when the stack is empty (pre-#27 / first-run wizard).
func (a *App) renderBody(height int) string {
	if p := a.topPage(); p != nil {
		return p.View(a.width, height)
	}
	return a.styles.Body.Default.
		Width(a.width).
		Height(height).
		Render("")
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

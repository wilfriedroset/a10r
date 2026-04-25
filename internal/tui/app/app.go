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
//
// TODO(#23): bind `Esc` at LayerGlobal to popPageMsg once the page
// stack lands. Today there's no stack, so an Esc with no prompt /
// modal in flight is a silent no-op.
func (a *App) registerGlobalBindings() {
	a.dispatcher.Set(keys.LayerGlobal, "Ctrl+C", func() tea.Cmd { return tea.Quit })
	a.dispatcher.Set(keys.LayerGlobal, "q", func() tea.Cmd { return tea.Quit })
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
		return a, nil
	case tea.QuitMsg:
		a.quitting = true
		return a, nil
	case keys.ChordExpiredMsg:
		return a, a.dispatcher.HandleChordExpired(m)
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
		return a, nil
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
	// Default: forward to Flash. Flash owns its own auto-clear ticks
	// (flashClearMsg) and a public FlashShowMsg, both of which need
	// to traverse the program loop. The Flash component no-ops on
	// unrecognised messages so this is safe as a catch-all.
	var cmd tea.Cmd
	a.flash, cmd = a.flash.Update(msg)
	return a, cmd
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
	_, cmd := a.dispatcher.Dispatch(keyName)
	return a, cmd
}

// View implements tea.Model. Composes header (top), body
// placeholder (middle), and footer (bottom) into a full-screen
// alt-screen view. Subcomponents render through theme.Styles so a
// theme swap re-paints without touching this code.
func (a *App) View() tea.View {
	if a.width == 0 || a.height == 0 {
		// Pre-resize: bubbletea's first WindowSizeMsg arrives in the
		// next loop iteration. Render an empty alt-screen view so we
		// don't crash with a zero-width Render.
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}

	headerLine := header.Render(header.State{Width: a.width}, a.styles)
	footerLines := a.renderFooter()

	bodyHeight := max(a.height-1-linesIn(footerLines), 0)
	body := a.styles.Body.Default.
		Width(a.width).
		Height(bodyHeight).
		Render("")

	out := lipgloss.JoinVertical(lipgloss.Left, headerLine, body, footerLines)
	v := tea.NewView(out)
	v.AltScreen = true
	return v
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

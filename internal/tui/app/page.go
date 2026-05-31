// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Page is one frame in the page stack. Update returns the Page
// interface (not a concrete type) because the stack stores them by
// interface; a concrete return would force a type switch on every call.
type Page interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Page, tea.Cmd)

	// Close releases the page's resources before it leaves the stack.
	// The app shell guarantees Close runs exactly once per page instance.
	Close() tea.Cmd

	View(width, height int) string
	Crumb() string

	// Title is the bordered body's top-edge label, k9s-style
	// "<resource>(<scope>)[<count>]". Empty falls back to Crumb.
	Title() string

	HeaderContent() string

	// Footer is the optional bordered body bottom-edge label, for
	// ambient state that belongs framed but doesn't deserve a body line.
	Footer() string

	Bindings() []action.Action
}

// ScopeChangedMsg announces that the user picked a new tenant scope.
// List pages observe it to filter snapshots and update their Title.
type ScopeChangedMsg struct {
	// Scope is "all", a single backend name, or a comma-joined subset
	// ("prod,staging") so title and filter logic need no new shape.
	Scope string
}

// GoToFirstRowMsg is the `gg` chord signal; list pages scroll the
// cursor home, others ignore it. Routed message types live here, not
// in keys/, so pages don't import keys just for the message type.
type GoToFirstRowMsg struct{}

// ClearMarksMsg is the `Ctrl+\` signal; pages with a marks map flash
// "marks cleared" when marks were set, others ignore it.
type ClearMarksMsg struct{}

// QuitRequestedMsg is the precursor every quit path emits instead of a
// bare tea.Quit Cmd. The App consumes it, runs each page's Close(), then
// emits tea.Quit.
//
// bubbletea intercepts tea.QuitMsg before Update, so returning tea.Quit
// directly would skip the page-stack tear-down and leak in-flight workers
// until their HTTP timeout; the precursor makes cleanup observable in Update.
type QuitRequestedMsg struct{}

// RefreshRequestedMsg is emitted on `r` to bypass the poll tick; the
// App routes it to the wiring layer's refresh func. Defined here so
// pages don't import poll just for the type.
type RefreshRequestedMsg struct {
	Resource string
	Scope    string
}

// PollAwarePage opts a page into resource-filtered cache replay on push:
// it lists the labels it reacts to (empty slice = no cached payload).
// Pages that don't implement it receive every cached payload.
type PollAwarePage interface {
	PollResources() []string
}

// TimeFormatChangedMsg announces a flip of the app-global time-format
// toggle so every view renders time consistently. The format vocabulary
// lives in timerender.
type TimeFormatChangedMsg struct {
	Format timerender.Format
}

// StateFormatToggleMsg is a page-emitted request to flip the app-global
// state-breakdown density. `Shift+T` is a page binding (not a global like
// `t`), so the page can't reach the canonical value and asks the App,
// which owns the truth, to flip it.
type StateFormatToggleMsg struct{}

// StateFormatChangedMsg announces a flip of the app-global state-breakdown
// density so dependent views render the same. Emitted by the App; the
// format vocabulary lives in stateformat.
type StateFormatChangedMsg struct {
	Format stateformat.Format
}

// pushPageMsg requests a push of the page produced by Factory. The
// factory shape (not a Page value) lets the page's Init run inside the
// App's Update so the returned Cmd reaches the program loop.
type pushPageMsg struct {
	Factory func() Page
}

// popPageMsg requests popping the top of the page stack.
type popPageMsg struct{}

// replacePageMsg replaces the top-of-stack page with Factory's output,
// for transitions that swap the active view without growing the stack.
type replacePageMsg struct {
	Factory func() Page
}

// PushPage returns a Cmd that requests pushing a new page. The factory
// is captured by reference so the page is constructed inside the App's
// Update cycle when the message is delivered.
func PushPage(factory func() Page) tea.Cmd {
	return func() tea.Msg { return pushPageMsg{Factory: factory} }
}

// PopPage returns a Cmd that requests popping the top page.
func PopPage() tea.Cmd {
	return func() tea.Msg { return popPageMsg{} }
}

// ReplacePage returns a Cmd that requests replacing the top page
// with the factory's output.
func ReplacePage(factory func() Page) tea.Cmd {
	return func() tea.Msg { return replacePageMsg{Factory: factory} }
}

// AutoPopMsg marks a message that should make the App pop the top page
// after delivering it to the parent. Forms tag their submitted/cancelled
// messages with it. The marker method's empty body forces explicit
// satisfaction so an unrelated tea.Msg can't accidentally match.
type AutoPopMsg interface {
	IsAutoPop()
}

// InputCapturePage is the optional interface a page implements to route
// every keystroke to itself, bypassing the dispatcher's LayerGlobal
// bindings (q, :, /, ?, 0-9) so forms can type those characters.
type InputCapturePage interface {
	Page
	CapturesInput() bool
}

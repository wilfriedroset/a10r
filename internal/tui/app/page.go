// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
)

// Page is one frame in the page stack. The App owns the stack;
// pages own their own state, dispatch their own messages, and
// describe their own crumbs / header content / bindings so the
// app shell stays page-agnostic.
//
// Update returns the same Page interface (not a concrete type)
// because the stack stores them by interface — a concrete-typed
// return would force every Update call to round-trip through a
// type switch. Either pointer or value receivers work; pages that
// mutate significant internal state in Update typically pick
// pointer receivers, while pure-data pages (status panes etc.)
// can stay value-typed.
type Page interface {
	// Init is called when the page is pushed onto the stack. The
	// returned Cmd runs in the App's Update cycle, so a page can
	// kick off polling, fetch its initial data, etc.
	Init() tea.Cmd

	// Update routes a message into the page. Returns a possibly-
	// derivative Page and an optional Cmd. Messages flow to the
	// top-of-stack page only; bottom pages are paused.
	Update(msg tea.Msg) (Page, tea.Cmd)

	// Close releases the page's resources before it leaves the
	// stack via pop or replace. The returned Cmd typically cancels
	// any background work the page started in Init (poller stops,
	// HTTP cancellations). nil is fine when nothing needs tearing
	// down. The app shell guarantees Close runs exactly once per
	// page instance — pages must not assume a paired Init.
	Close() tea.Cmd

	// View renders the page body to fit width × height. The app
	// shell composes the result with the header above and the
	// footer below.
	View(width, height int) string

	// Crumb is the breadcrumb label rendered in the footer's crumb
	// strip. The bottom page is rendered first; the top page wears
	// the active highlight.
	Crumb() string

	// HeaderContent is the per-view middle-zone slot string per the
	// J1 header spec. Empty omits the slot.
	HeaderContent() string

	// Bindings returns the page's hint-strip actions (already
	// filtered for read-only mode by the registry, if applicable).
	// The app shell rebuilds the hint strip on every render from
	// the top-of-stack page.
	Bindings() []action.Action
}

// pushPageMsg requests a push of the page produced by Factory.
// The factory shape (instead of a Page value) lets the page's
// Init run inside the App's Update — required by bubbletea
// convention so the returned Cmd reaches the program loop.
type pushPageMsg struct {
	Factory func() Page
}

// popPageMsg requests popping the top of the page stack. Popping
// when the stack has one or zero pages is a no-op so a stray Esc
// at the home view never crashes.
type popPageMsg struct{}

// replacePageMsg replaces the top-of-stack page with the page
// produced by Factory. Used by transitions that swap the active
// view without growing the stack (e.g. tenant picker → reopen the
// invoking view with the new tenant set).
type replacePageMsg struct {
	Factory func() Page
}

// PushPage returns a Cmd that requests pushing a new page. The
// factory is captured by reference so the page is constructed
// inside the App's Update cycle when the message is delivered.
// Public so pages and tests can request transitions without
// reaching into app's internals.
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

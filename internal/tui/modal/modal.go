// SPDX-License-Identifier: Apache-2.0

// Package modal hosts the overlay surfaces that take precedence
// over the page stack: the tenant picker (Ctrl+T per C3) and the
// generic yes/no confirm dialog used by destructive flows like
// silence expire.
//
// Modals are value-typed and self-contained: they own their key
// handling rather than registering at the dispatcher's LayerModal.
// The app shell routes input to whichever modal is open before
// reaching the dispatcher; this keeps the modal package free of
// dispatcher coupling and makes each modal trivially unit-testable
// without booting the keys layer.
//
// Why not LayerModal? keybindings.md treats "modals" as the top
// of the precedence stack. The dispatcher implements that as a
// real layer (keys.LayerModal), but today the App owns modal
// routing end-to-end so the layer slot is reserved for a future
// feature: cross-modal app-level bindings (e.g. a global "panic
// quit" key the user can press inside any modal). For v0.1 the
// modal slot is sufficient; LayerModal stays empty.
package modal

import (
	tea "charm.land/bubbletea/v2"
)

// Modal is the contract every overlay implements. The App keeps a
// single Modal field; when non-nil it captures every input event
// until Update returns a "I'm done" signal (a closing message
// that the App acts on by setting the field back to nil).
//
// View renders into the dimensions the App passes — typically the
// full body region. Centering / framing is the modal's call so a
// future picker can claim the full screen if it wants.
type Modal interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Modal, tea.Cmd)
	View(width, height int) string

	// Title is the label rendered in the App's outer panel border
	// when the modal is open. Implementations return short, lower-
	// case labels: "Help", "tenant", "confirm". Empty falls back
	// to a generic placeholder in the App shell.
	Title() string
}

// ResultMsg marks every modal-resolution message. The App-shell's
// auto-close path switches on this interface so any future modal
// type whose result implements ResultMsg gets correct routing for
// free — the App does NOT enumerate the concrete result types
// directly. Picker, Confirm, and Help result types implement it.
type ResultMsg interface {
	// IsModalResult is the marker method. The empty body makes
	// satisfaction explicit (an unrelated tea.Msg can't accidentally
	// match) while still letting modals declared in other packages
	// — like the help overlay — implement the interface.
	IsModalResult()
}

// HelpClosedMsg is emitted when the help overlay is dismissed.
// Lives in this package (rather than internal/tui/help) so that
// other modals don't need to reach across packages to satisfy
// ResultMsg.
type HelpClosedMsg struct{}

// IsModalResult satisfies ResultMsg.
func (HelpClosedMsg) IsModalResult() {}

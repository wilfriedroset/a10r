// SPDX-License-Identifier: Apache-2.0

// Package modal hosts the async-result overlay surfaces that take
// precedence over the page stack: the tenant picker (Ctrl+T) and
// the generic yes/no confirm dialog used by destructive flows like
// silence expire. Each carries a typed result
// (`PickerSubmittedMsg`, `ConfirmResultMsg`, ...) the caller acts
// on.
//
// Viewer overlays (a surface that only renders information for as
// long as the user looks at it) live in their own packages with
// their own routing slot on the app shell; see internal/tui/help and
// ADR 0020 for the split.
//
// Modals are value-typed and self-contained: they own their key
// handling rather than registering at the dispatcher's LayerModal.
// The app shell routes input to whichever modal is open before
// reaching the dispatcher; this keeps the modal package free of
// dispatcher coupling and makes each modal trivially unit-testable
// without booting the keys layer. keys.LayerModal stays empty —
// the App owns modal routing end-to-end.
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
// full body region. Centering / framing is the modal's call.
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
// auto-close path switches on this interface so any modal whose
// result implements ResultMsg gets correct routing without the App
// enumerating concrete types. Picker and Confirm result types
// implement it.
type ResultMsg interface {
	// IsModalResult marks the type. Explicit method (rather than an
	// embedded sentinel) lets modals declared in other packages
	// implement the interface without importing this one.
	IsModalResult()
}

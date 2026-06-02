// SPDX-License-Identifier: Apache-2.0

// Package modal hosts the async-result overlay surfaces that take
// precedence over the page stack (tenant picker, yes/no confirm), each
// carrying a typed result the caller acts on. Viewer overlays live in
// their own packages (see internal/tui/help and ADR 0020 for the split).
//
// Modals own their key handling rather than registering at the
// dispatcher's LayerModal: the App routes input to the open modal before
// reaching the dispatcher, which keeps this package dispatcher-free and
// each modal unit-testable without booting the keys layer.
package modal

import (
	tea "charm.land/bubbletea/v2"
)

// Modal is the contract every overlay implements. The App keeps a single
// Modal field; when non-nil it captures every input event until Update
// returns a closing message that the App acts on by setting the field
// back to nil. View renders into the dimensions the App passes.
type Modal interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Modal, tea.Cmd)
	View(width, height int) string

	// Title is the short label rendered in the App's outer panel border;
	// empty falls back to a generic placeholder.
	Title() string
}

// ResultMsg marks every modal-resolution message so the App's auto-close
// path can route any modal's result without enumerating concrete types.
type ResultMsg interface {
	// IsModalResult marks the type. An explicit method rather than an
	// embedded sentinel lets modals in other packages implement the
	// interface without importing this one.
	IsModalResult()
}

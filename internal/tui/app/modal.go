// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// PickerOriginScope tags the global tenant-quick-switch picker
// opened by Ctrl+T so its submission folds into the App's scope
// state. Pickers opened by individual pages (e.g. the silence
// form's tenant row) use their own Origin string and the App
// forwards their result through unchanged.
const PickerOriginScope = "scope"

// openModalMsg requests opening the modal produced by Factory.
// Factory shape (rather than a Modal value) lets the modal's Init
// run inside the App's Update cycle, matching the pattern push
// pages use.
type openModalMsg struct {
	Factory func() modal.Modal
}

// closeModalMsg requests dismissing the open modal. Tests use this
// to drive the close path without forging a key event.
type closeModalMsg struct{}

// OpenModal returns a Cmd that opens a modal. The factory is
// captured by reference so the constructed modal's Init runs
// inside the App's Update cycle and the returned Cmd reaches the
// program loop.
func OpenModal(factory func() modal.Modal) tea.Cmd {
	return func() tea.Msg { return openModalMsg{Factory: factory} }
}

// CloseModal returns a Cmd that dismisses the open modal. Pages /
// tests rarely need this directly: the App auto-closes when a
// modal emits a Submitted / Cancelled / ConfirmResult message.
func CloseModal() tea.Cmd {
	return func() tea.Msg { return closeModalMsg{} }
}

// openModal sets the modal field to the factory's output and runs
// its Init. nil-factory and nil-product are no-ops, matching push
// page semantics.
func (a *App) openModal(factory func() modal.Modal) tea.Cmd {
	if factory == nil {
		return nil
	}
	m := factory()
	if m == nil {
		return nil
	}
	a.overlays.modal = m
	return m.Init()
}

// closeModal clears the modal slot. Idempotent — a redundant close
// (e.g. CloseModal Cmd plus a result message arriving in the same
// frame) is harmless.
func (a *App) closeModal() {
	a.overlays.modal = nil
}

// isModalResult reports whether msg is a modal-resolution message
// that should auto-close the open modal. Uses the modal.ResultMsg
// marker interface so any future modal whose result type
// implements that interface gets correct routing for free — no
// extra wiring in this file required.
func isModalResult(msg tea.Msg) bool {
	_, ok := msg.(modal.ResultMsg)
	return ok
}

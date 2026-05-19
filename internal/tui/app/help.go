// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/help"
)

// openHelpMsg requests opening the help overlay with the supplied
// options. Unlike openModalMsg, no factory indirection is needed:
// help.Help has no Init-time cmd to defer (per ADR 0020), so the
// options are captured by value at request time and the app stores
// the constructed overlay directly when the message lands.
type openHelpMsg struct {
	Options help.Options
}

// OpenHelp returns a Cmd that opens the help overlay. The options
// are captured at call time so the RESOURCE column reflects whichever
// page is on top of the stack the moment `?` was pressed.
func OpenHelp(opts help.Options) tea.Cmd {
	return func() tea.Msg { return openHelpMsg{Options: opts} }
}

// openHelp installs the overlay in the help slot. Idempotent — a
// second `?` while help is already open is structurally impossible
// (handleKey routes keys to the open overlay rather than the
// dispatcher), but the assignment is overwrite-not-append either way.
func (a *App) openHelp(opts help.Options) {
	a.help = help.New(opts)
}

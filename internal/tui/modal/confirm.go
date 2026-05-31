// SPDX-License-Identifier: Apache-2.0

package modal

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ConfirmDefault selects which choice the Enter key resolves to.
type ConfirmDefault int

const (
	// ConfirmDefaultNo treats Enter as "no" — the safe default for
	// destructive actions.
	ConfirmDefaultNo ConfirmDefault = iota
	// ConfirmDefaultYes treats Enter as "yes"; only for non-destructive
	// confirmations.
	ConfirmDefaultYes
)

// ConfirmResultMsg carries the user's answer; Cancelled is true on Esc.
type ConfirmResultMsg struct {
	Yes       bool
	Cancelled bool
}

// IsModalResult satisfies ResultMsg.
func (ConfirmResultMsg) IsModalResult() {}

// Confirm is a yes/no dialog. Default-No matches keybindings.md, where
// every destructive flow shows a confirm before acting.
type Confirm struct {
	question string
	def      ConfirmDefault
}

// NewConfirm builds a confirm modal with the given question and default
// choice.
func NewConfirm(question string, def ConfirmDefault) *Confirm {
	return &Confirm{question: question, def: def}
}

func (*Confirm) Init() tea.Cmd { return nil }

// Title implements Modal.
func (*Confirm) Title() string { return "confirm" }

// Update implements Modal. Recognises y/Y, n/N, Enter (resolves the
// default), Esc (cancel). Other keys are silently ignored so a stray
// keystroke can't accidentally pick a destructive option.
func (c *Confirm) Update(msg tea.Msg) (Modal, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return c, nil
	}
	switch keyMsg.String() {
	case "y", "Y":
		return c, func() tea.Msg { return ConfirmResultMsg{Yes: true} }
	case "n", "N":
		return c, func() tea.Msg { return ConfirmResultMsg{Yes: false} }
	case "enter":
		yes := c.def == ConfirmDefaultYes
		return c, func() tea.Msg { return ConfirmResultMsg{Yes: yes} }
	case "esc":
		return c, func() tea.Msg { return ConfirmResultMsg{Cancelled: true} }
	}
	return c, nil
}

func (c *Confirm) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	hint := "[y]es / [n]o   Enter=" + defaultLabel(c.def) + "   Esc=cancel"
	body := strings.Join([]string{c.question, "", hint}, "\n")
	return lipgloss.NewStyle().Width(width).Render(body)
}

func defaultLabel(d ConfirmDefault) string {
	if d == ConfirmDefaultYes {
		return "yes"
	}
	return "no"
}

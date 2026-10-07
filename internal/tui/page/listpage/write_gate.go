// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// WritePolicy is the slice of the session the write gate reads.
type WritePolicy interface {
	ReadOnly() bool
	Guardrails() guardrail.Set
}

// GateWrite runs action behind the TUI write gate. Read-only goes
// first and always wins, so a rule is never quoted on a backend
// nothing can write to and a refused press leaves an open range
// alone. commit, when set, closes that range before req is built: an
// open range is not marked yet, and the policy has to count the rows
// the press would really reach. A refusal keeps the marks on purpose,
// so the user can narrow them.
func GateWrite(s WritePolicy, readOnlyHint string, commit func(), req func() guardrail.Request, action func() tea.Cmd) tea.Cmd {
	if s.ReadOnly() {
		return footer.ShowFlash(footer.FlashWarn, readOnlyHint)
	}
	if commit != nil {
		commit()
	}
	if d := s.Guardrails().Decide(req()); d.Refused() {
		return footer.ShowFlash(footer.FlashWarn, d.Flash())
	}
	return action()
}

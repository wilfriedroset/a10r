// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// OpenBulkForm runs form directly for a single target, or behind a
// confirm asking question. A single target skips the blast-radius
// question but not a guardrail one: the bulk form leaves policy to
// the page, so nothing downstream would ask on its behalf.
func OpenBulkForm(targets int, d guardrail.Decision, question string, form func() tea.Cmd) tea.Cmd {
	if targets == 1 && d.Confirm == "" {
		return form()
	}
	return app.OpenModal(func() modal.Modal {
		return modal.NewGuardedConfirm(question, modal.ConfirmDefaultYes, d.Typed)
	})
}

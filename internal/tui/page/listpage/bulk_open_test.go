// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
)

type formPushed struct{}

func TestOpenBulkForm(t *testing.T) {
	t.Parallel()

	form := func() tea.Cmd { return func() tea.Msg { return formPushed{} } }
	tests := []struct {
		name      string
		targets   int
		decision  guardrail.Decision
		wantForm  bool
		wantModal modal.Modal
	}{
		{name: "single target without a guardrail skips the confirm", targets: 1, wantForm: true},
		{name: "several targets ask the blast-radius question", targets: 2, wantModal: &modal.Confirm{}},
		{
			name: "a plain guardrail asks on a single target", targets: 1,
			decision:  guardrail.Decision{Confirm: guardrail.ConfirmationPlain},
			wantModal: &modal.Confirm{},
		},
		{
			name: "a typed guardrail asks for the tenant on a single target", targets: 1,
			decision:  guardrail.Decision{Confirm: guardrail.ConfirmationTypeTenantName, Typed: []string{"prod"}},
			wantModal: &modal.TypedConfirm{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := listpage.OpenBulkForm(tt.targets, tt.decision, "silence?", form)
			if tt.wantForm {
				require.IsType(t, formPushed{}, cmd())
				return
			}
			m := pagetest.OpenedModal(t, cmd)
			require.IsType(t, tt.wantModal, m)
			require.Contains(t, m.View(70, 14), "silence?")
		})
	}
}

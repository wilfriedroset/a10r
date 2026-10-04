// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/session"
)

type actionRan struct{}

func TestGateWrite(t *testing.T) {
	t.Parallel()

	deny := guardrail.Set{{Tenants: []string{"prod"}, Actions: []string{"silence.create"}, Deny: true}}
	tests := []struct {
		name      string
		cfg       config.Config
		nilCommit bool
		wantCalls []string
		wantFlash string
	}{
		{
			name:      "read-only refuses before the range is committed",
			cfg:       config.Config{Defaults: config.Defaults{ReadOnly: true}, Guardrails: deny},
			wantFlash: "read-only",
		},
		{
			name:      "a deny refuses after the commit and before the action",
			cfg:       config.Config{Guardrails: deny},
			wantCalls: []string{"commit", "request"},
			wantFlash: "silence.create denied on prod",
		},
		{
			name:      "an allowed write asks on the committed rows, then acts",
			wantCalls: []string{"commit", "request", "action"},
		},
		{
			name:      "a nil commit is skipped",
			nilCommit: true,
			wantCalls: []string{"request", "action"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			commit := func() { calls = append(calls, "commit") }
			if tt.nilCommit {
				commit = nil
			}
			cmd := listpage.GateWrite(session.New(tt.cfg), "read-only", commit,
				func() guardrail.Request {
					calls = append(calls, "request")
					return guardrail.Request{Action: guardrail.ActionSilenceCreate, Tenants: []string{"prod"}}
				},
				func() tea.Cmd {
					calls = append(calls, "action")
					return func() tea.Msg { return actionRan{} }
				})

			require.Equal(t, tt.wantCalls, calls)
			if tt.wantFlash == "" {
				require.IsType(t, actionRan{}, cmd())
				return
			}
			msg, ok := cmd().(footer.FlashShowMsg)
			require.True(t, ok)
			require.Equal(t, footer.FlashWarn, msg.Level)
			require.Equal(t, tt.wantFlash, msg.Text)
		})
	}
}

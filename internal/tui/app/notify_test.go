// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/notify"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func notifyApp(t *testing.T, scope string) *App {
	t.Helper()
	return NewApp(Options{
		Styles:     testutil.LoadStyles(t),
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
		Scope:      scope,
		Notify:     notify.New(config.Notify{Enabled: true}),
	})
}

func firingMsg(tenant, alertname string) poll.DataMsg {
	return poll.DataMsg{
		Tenant:        tenant,
		ResourceLabel: "alerts",
		Resource: []backend.Alert{{
			Labels: map[string]string{"alertname": alertname, "severity": "critical"},
			State:  backend.AlertStateActive,
		}},
	}
}

// notifyFlash walks the Cmd a single Update returned and reports the
// flash text the notifier raised, or "" when it raised none.
func notifyFlash(cmd tea.Cmd) string {
	for _, msg := range flatten(cmd) {
		if f, ok := msg.(footer.FlashShowMsg); ok {
			return f.Text
		}
	}
	return ""
}

func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, flatten(c)...)
	}
	return out
}

func TestApp_NotifyAnnouncesNewFiringAlerts(t *testing.T) {
	t.Parallel()
	a := notifyApp(t, "")

	_, cmd := a.Update(poll.DataMsg{Tenant: "prod", ResourceLabel: "alerts", Resource: []backend.Alert{}})
	require.Empty(t, notifyFlash(cmd), "the first poll of a tenant warms up")

	_, cmd = a.Update(firingMsg("prod", "HighLatency"))
	require.Equal(t, "prod: HighLatency (critical)", notifyFlash(cmd))
}

func TestApp_NotifyIgnoresPollsItCannotRead(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		msg  poll.DataMsg
	}{
		{
			name: "another resource",
			msg:  poll.DataMsg{Tenant: "prod", ResourceLabel: "silences", Resource: []backend.Silence{{ID: "a"}}},
		},
		{
			name: "no resource label",
			msg:  poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{}},
		},
		{
			name: "a payload of another type",
			msg:  poll.DataMsg{Tenant: "prod", ResourceLabel: "alerts", Resource: "not alerts"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := notifyApp(t, "")
			_, cmd := a.Update(tc.msg)
			require.Empty(t, notifyFlash(cmd))
			// A tenant the notifier never saw still warms up, which
			// proves the ignored poll did not seed it.
			_, cmd = a.Update(firingMsg("prod", "HighLatency"))
			require.Empty(t, notifyFlash(cmd))
		})
	}
}

func TestApp_NotifyStartsOnTheBootScope(t *testing.T) {
	t.Parallel()
	a := notifyApp(t, "prod")

	_, cmd := a.Update(poll.DataMsg{Tenant: "staging", ResourceLabel: "alerts", Resource: []backend.Alert{}})
	require.Empty(t, notifyFlash(cmd))
	_, cmd = a.Update(firingMsg("staging", "HighLatency"))
	require.Empty(t, notifyFlash(cmd), "a tenant outside the boot scope never notifies")
}

func TestApp_NotifyScopeChangeReWarmsATenant(t *testing.T) {
	t.Parallel()
	a := notifyApp(t, "")

	_, cmd := a.Update(poll.DataMsg{Tenant: "prod", ResourceLabel: "alerts", Resource: []backend.Alert{}})
	require.Empty(t, notifyFlash(cmd))

	_, _ = a.Update(ScopeChangedMsg{Scope: "staging"})
	_, cmd = a.Update(firingMsg("prod", "HighLatency"))
	require.Empty(t, notifyFlash(cmd), "prod left the scope")

	_, _ = a.Update(ScopeChangedMsg{Scope: "all"})
	_, cmd = a.Update(firingMsg("prod", "HighLatency"))
	require.Empty(t, notifyFlash(cmd), "prod warms up again on its return")
	_, cmd = a.Update(firingMsg("prod", "DiskFull"))
	require.Equal(t, "prod: DiskFull (critical)", notifyFlash(cmd))
}

func TestApp_NilNotifierIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	_, cmd := a.Update(firingMsg("prod", "HighLatency"))
	require.Empty(t, notifyFlash(cmd))
	_, _ = a.Update(ScopeChangedMsg{Scope: "prod"})
}

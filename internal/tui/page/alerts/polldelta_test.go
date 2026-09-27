// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/session"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func newDeltaPage(t *testing.T, tenants []string) *Page {
	t.Helper()
	return New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Session: session.New(config.Config{TUI: config.TUI{PollDelta: true}}),
		Tenants: tenants,
	})
}

// deltaAlerts turns alertnames into one instance each — the delta
// only ever looks at the alertname key set.
func deltaAlerts(names ...string) []backend.Alert {
	out := make([]backend.Alert, 0, len(names))
	for i, n := range names {
		out = append(out, mkAlert(n, "warning", backend.AlertStateActive, string(rune('a'+i)), time.Minute, nil))
	}
	return out
}

// feed replays the polls and returns the command the last one produced.
func feed(p *Page, polls []poll.DataMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, m := range polls {
		_, cmd = p.Update(m)
	}
	return cmd
}

func requireFlash(t *testing.T, cmd tea.Cmd, wantText string, wantLevel footer.FlashLevel) {
	t.Helper()
	require.NotNil(t, cmd, "a changed poll must flash")
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a poll delta surfaces as a flash")
	require.Equal(t, wantText, msg.Text)
	require.Equal(t, wantLevel, msg.Level)
	require.True(t, msg.Weak, "a poll delta must yield to fresh user feedback")
}

func TestPollDelta_CountsAggregateKeys(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		polls     [][]string
		wantText  string
		wantLevel footer.FlashLevel
	}{
		{name: "first poll is silent", polls: [][]string{{"HighCPU"}}},
		{name: "no change is silent", polls: [][]string{{"HighCPU"}, {"HighCPU"}}},
		{name: "new aggregate", polls: [][]string{{"HighCPU"}, {"HighCPU", "DiskFull"}}, wantText: "+1 new", wantLevel: footer.FlashWarn},
		{name: "resolved aggregate", polls: [][]string{{"HighCPU", "DiskFull"}, {"HighCPU"}}, wantText: "-1 resolved", wantLevel: footer.FlashSuccess},
		{name: "new and resolved", polls: [][]string{{"HighCPU", "DiskFull"}, {"HighCPU", "OOMKilled", "NetDown"}}, wantText: "+2 new, -1 resolved", wantLevel: footer.FlashWarn},
		{name: "empty first poll then alerts", polls: [][]string{{}, {"HighCPU"}}, wantText: "+1 new", wantLevel: footer.FlashWarn},
		{name: "everything resolved", polls: [][]string{{"HighCPU", "DiskFull"}, {}}, wantText: "-2 resolved", wantLevel: footer.FlashSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newDeltaPage(t, nil)
			msgs := make([]poll.DataMsg, 0, len(tc.polls))
			for _, names := range tc.polls {
				msgs = append(msgs, poll.DataMsg{Resource: deltaAlerts(names...)})
			}
			cmd := feed(p, msgs)
			if tc.wantText == "" {
				require.Nil(t, cmd)
				return
			}
			requireFlash(t, cmd, tc.wantText, tc.wantLevel)
		})
	}
}

// A count change inside one aggregate is not a delta: the spec
// reports appearing and disappearing alertnames only.
func TestPollDelta_CountChangeInsideAnAggregateIsSilent(t *testing.T) {
	t.Parallel()

	p := newDeltaPage(t, nil)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
	}})
	_, cmd := p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		mkAlert("HighCPU", "critical", backend.AlertStateActive, "fp2", time.Minute, nil),
	}})
	require.Nil(t, cmd)
}

func TestPollDelta_AlertnameMovingBetweenTenants(t *testing.T) {
	t.Parallel()

	p := newDeltaPage(t, []string{"prod", "stg"})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: deltaAlerts("HighCPU")})
	_, _ = p.Update(poll.DataMsg{Tenant: "stg", Resource: deltaAlerts()})

	_, gone := p.Update(poll.DataMsg{Tenant: "prod", Resource: deltaAlerts()})
	requireFlash(t, gone, "prod: -1 resolved", footer.FlashSuccess)

	_, arrived := p.Update(poll.DataMsg{Tenant: "stg", Resource: deltaAlerts("HighCPU")})
	requireFlash(t, arrived, "stg: +1 new", footer.FlashWarn)
}

func TestPollDelta_SingleTenantOmitsThePrefix(t *testing.T) {
	t.Parallel()

	p := newDeltaPage(t, []string{"prod"})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: deltaAlerts()})
	_, cmd := p.Update(poll.DataMsg{Tenant: "prod", Resource: deltaAlerts("HighCPU")})
	requireFlash(t, cmd, "+1 new", footer.FlashWarn)
}

func TestPollDelta_SilentWhenDisabled(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }, Session: testutil.Session()})
	_, _ = p.Update(poll.DataMsg{Resource: deltaAlerts()})
	_, cmd := p.Update(poll.DataMsg{Resource: deltaAlerts("HighCPU")})
	require.Nil(t, cmd, "tui.poll_delta off must flash nothing")
}

func TestPollDelta_SilentWhenPausedDropsTheMessage(t *testing.T) {
	t.Parallel()

	p := newDeltaPage(t, nil)
	_, _ = p.Update(poll.DataMsg{Resource: deltaAlerts()})
	p.Paused = true

	_, cmd := p.Update(poll.DataMsg{Resource: deltaAlerts("HighCPU")})
	require.Nil(t, cmd, "a dropped poll is not a poll")

	p.PausedRefresh = true
	_, cmd = p.Update(poll.DataMsg{Resource: deltaAlerts("HighCPU")})
	requireFlash(t, cmd, "+1 new", footer.FlashWarn)
}

func TestPollDelta_SilentForAnOutOfScopeTenant(t *testing.T) {
	t.Parallel()

	p := newDeltaPage(t, []string{"prod", "stg"})
	p.Scope = "prod"
	_, _ = p.Update(poll.DataMsg{Tenant: "stg", Resource: deltaAlerts()})
	_, cmd := p.Update(poll.DataMsg{Tenant: "stg", Resource: deltaAlerts("HighCPU")})
	require.Nil(t, cmd, "a tenant outside the scope is not on screen")
}

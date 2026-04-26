// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

func sampleGroups() []backend.AlertGroup {
	return []backend.AlertGroup{
		{
			Labels: map[string]string{"team": "platform"},
			Alerts: []backend.Alert{
				{Labels: map[string]string{"alertname": "A", "team": "platform", "severity": "critical"}},
				{Labels: map[string]string{"alertname": "B", "team": "platform", "severity": "warning"}},
			},
		},
		{
			Labels: map[string]string{"team": "data"},
			Alerts: []backend.Alert{
				{Labels: map[string]string{"alertname": "C", "team": "data"}},
			},
		},
	}
}

func TestPage_StartsCollapsed(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	rows := p.rows()
	require.Len(t, rows, 2, "every group is collapsed → one row per group")
}

func TestPage_EnterTogglesExpand(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, p.expanded[0])
	require.Len(t, p.rows(), 4, "expanded first group adds 2 leaf rows")

	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, p.expanded[0])
	require.Len(t, p.rows(), 2)
}

func TestPage_TabExpandsAll(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	for i, e := range p.expanded {
		require.True(t, e, "group %d must be expanded after Tab", i)
	}

	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	for i, e := range p.expanded {
		require.False(t, e, "group %d must collapse after second Tab", i)
	}
}

func TestPage_EnterOnLeafEmitsDrillAlert(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // expand group 0
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(DrillAlertMsg)
	require.Equal(t, "A", msg.Alert.Labels["alertname"])
}

func TestPage_SilenceEmitsCommonLabels(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	msg := cmd().(DrillSilenceMsg)
	// team=platform is shared by both alerts in group 0.
	require.Equal(t, "platform", msg.CommonLabels["team"])
	// alertname / severity differ between A and B → must NOT be in
	// the intersection.
	_, hasName := msg.CommonLabels["alertname"]
	require.False(t, hasName)
	_, hasSev := msg.CommonLabels["severity"]
	require.False(t, hasSev)
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, len(p.rows())-1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Equal(t, 0, p.cursor)
}

func TestPage_RenderShowsGroupLabelsAndAlertCount(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	out := p.View(80, 10)
	require.Contains(t, out, "team=platform")
	require.Contains(t, out, "(2 alerts)")
}

func TestCommonLabels_EmptyInput(t *testing.T) {
	t.Parallel()
	require.Empty(t, commonLabels(nil))
}

func TestPage_FilterPromptIsLive(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	// sampleGroups has two entries: team=platform and team=data.
	// Filter by "data" → only the data group is visible.
	_, _ = p.Update(footer.PromptOpenedMsg{Mode: footer.PromptFilter})
	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "data"})
	require.Equal(t, "groups[1/2]", p.Title())
	visible := p.visibleGroups()
	require.Len(t, visible, 1)
	require.Equal(t, "data", visible[0].Labels["team"])

	// Cancel reverts.
	_, _ = p.Update(footer.PromptCancelledMsg{Mode: footer.PromptFilter})
	require.Empty(t, p.filter)
	require.Equal(t, "groups[2]", p.Title())
}

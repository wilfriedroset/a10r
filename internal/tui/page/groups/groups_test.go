// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
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
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	rows := p.rows()
	require.Len(t, rows, 2, "every group is collapsed → one row per group")
}

func TestPage_EnterTogglesExpand(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
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
	p := New(Options{Styles: loadStyles(t)})
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
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // expand group 0
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(DrillAlertMsg)
	require.Equal(t, "A", msg.Alert.Labels["alertname"])
}

func TestPage_SilenceWithoutClientsFlashesHint(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: "prod"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend",
		"`s` with no clients must explain rather than push a broken form")
}

func TestPage_SilencePushesFormPrefilledWithCommonLabels(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  loadStyles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
		Creator: "wilfried",
	})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: "prod"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd, "`s` with clients must push the form")
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "`s` with clients must push the form, not flash")
}

func TestPage_SilenceFormSubmittedFlashesSuccess(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, cmd := p.Update(silenceform.SubmittedMsg{ID: "sil-99"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence created: sil-99")
}

func TestPage_SilenceOnEmptyViewIsNoop(t *testing.T) {
	t.Parallel()
	// No DataMsg → empty rows → `s` flashes "no group under cursor".
	p := New(Options{
		Styles:  loadStyles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no group under the cursor")
}

// fakeSilenceClient satisfies silenceform.Client; the groups
// page never calls its methods in tests (the `s` push test
// asserts only that a non-flash Cmd is produced).
type fakeSilenceClient struct{}

func (*fakeSilenceClient) CreateSilence(_ context.Context, _ backend.SilenceSpec) (string, error) {
	return "fake-silence-id", nil
}

func (*fakeSilenceClient) UpdateSilence(_ context.Context, _ string, _ backend.SilenceSpec) error {
	return nil
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, len(p.rows())-1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Equal(t, 0, p.cursor)
}

func TestPage_RenderShowsGroupLabelsAndAlertCount(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	out := p.View(80, 10)
	require.Contains(t, out, "team=platform")
	require.Contains(t, out, "(2 alerts)")
}

func TestCommonLabels_EmptyInput(t *testing.T) {
	t.Parallel()
	require.Empty(t, commonLabels(nil))
}

func TestCommonLabels_KeepsSharedDropsDivergent(t *testing.T) {
	t.Parallel()
	// Two alerts agree on team=platform; alertname and severity
	// differ. Only the shared key/value belongs in the silence
	// matchers — divergent labels would over-narrow the silence.
	alerts := []backend.Alert{
		{Labels: map[string]string{"alertname": "A", "team": "platform", "severity": "critical"}},
		{Labels: map[string]string{"alertname": "B", "team": "platform", "severity": "warning"}},
	}
	got := commonLabels(alerts)
	require.Equal(t, map[string]string{"team": "platform"}, got)
}

func TestCommonLabels_AllSharedSurvives(t *testing.T) {
	t.Parallel()
	// Identical label-sets → every key survives.
	alerts := []backend.Alert{
		{Labels: map[string]string{"team": "platform", "env": "prod"}},
		{Labels: map[string]string{"team": "platform", "env": "prod"}},
	}
	got := commonLabels(alerts)
	require.Equal(t, map[string]string{"team": "platform", "env": "prod"}, got)
}

func TestPage_FilterPromptIsLive(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	// sampleGroups has two entries: team=platform and team=data.
	// Filter by "data" → only the data group is visible.
	_, _ = p.Update(footer.PromptOpenedMsg{Mode: footer.PromptFilter})
	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "data"})
	require.Equal(t, "groups(all)[1/2]", p.Title())
	visible := p.visibleGroups()
	require.Len(t, visible, 1)
	require.Equal(t, "data", visible[0].Labels["team"])

	// Cancel reverts.
	_, _ = p.Update(footer.PromptCancelledMsg{Mode: footer.PromptFilter})
	require.Empty(t, p.filter)
	require.Equal(t, "groups(all)[2]", p.Title())
}

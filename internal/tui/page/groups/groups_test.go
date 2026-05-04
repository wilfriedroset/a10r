// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
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

// stripStyle drops ANSI SGR sequences for substring assertions
// against rendered output.
func stripStyle(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestPage_TitleColdStartShowsLoading(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	require.Contains(t, stripStyle(p.Title()), "loading groups",
		"cold-start title must read as loading until the first DataMsg lands")
}

func TestPage_TitleAfterDataMsgFlipsToCount(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: ""})
	require.Equal(t, "groups(all)[2]", p.Title())
}

func TestPage_RefreshKeyEmitsRequestAndFlipsRefreshing(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: ""})
	require.False(t, p.refreshing)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.True(t, p.refreshing,
		"`r` must flip the page into refreshing state")
	require.NotNil(t, cmd)

	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	var sawRefresh bool
	for _, c := range batch {
		if rr, ok := c().(app.RefreshRequestedMsg); ok {
			require.Equal(t, "groups", rr.Resource)
			require.Equal(t, "all", rr.Scope)
			sawRefresh = true
		}
	}
	require.True(t, sawRefresh)
}

func TestPage_FooterShowsRefreshingThenNextRefresh(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	p := New(Options{Styles: loadStyles(t), Now: func() time.Time { return now }})
	require.Empty(t, p.Footer())

	_, _ = p.Update(poll.DataMsg{
		Resource: sampleGroups(),
		Tenant:   "",
		NextAt:   now.Add(25 * time.Second),
	})
	require.Equal(t, "next refresh 25s", p.Footer())

	_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Equal(t, "refreshing…", p.Footer())
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
	// `gg` is the chord — the dispatcher consumes the first `g`,
	// then resolves to GoToFirstRowMsg on the second. Tests inject
	// the resolved message directly so the assertion is independent
	// of the chord buffer.
	_, _ = p.Update(app.GoToFirstRowMsg{})
	require.Equal(t, 0, p.cursor)
}

func TestPage_FullPageMotionsMoveCursor(t *testing.T) {
	t.Parallel()

	// Build enough rows that the cold-start fallback (20) lands inside
	// the row list without clamping. Each group is collapsed by default
	// → one row per group.
	p := New(Options{Styles: loadStyles(t)})
	gs := make([]backend.AlertGroup, 60)
	for i := range gs {
		gs[i] = backend.AlertGroup{
			Labels: map[string]string{"team": "t" + string(rune('a'+(i%26)))},
			Alerts: []backend.Alert{{Labels: map[string]string{"alertname": "A"}}},
		}
	}
	_, _ = p.Update(poll.DataMsg{Resource: gs})

	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "cold-start Ctrl+F falls back to 20 rows")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+B mirrors Ctrl+F")

	// Ctrl+D / Ctrl+U mirror with the half-step fallback.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 10, p.cursor, "cold-start Ctrl+D falls back to 10 rows")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D")

	// Clamps at edges — Ctrl+F at the bottom stays put.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, len(p.rows())-1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, len(p.rows())-1, p.cursor,
		"Ctrl+F at the last row clamps; never overshoots")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()

	// After a render the page snapshots its body-row budget. Ctrl+D
	// must walk half the viewport, Ctrl+F a full window minus two —
	// vim's CTRL-F overlap convention.
	p := New(Options{Styles: loadStyles(t)})
	gs := make([]backend.AlertGroup, 100)
	for i := range gs {
		gs[i] = backend.AlertGroup{
			Labels: map[string]string{"team": "t" + string(rune('a'+(i%26))), "i": string(rune('0' + (i % 10)))},
			Alerts: []backend.Alert{{Labels: map[string]string{"alertname": "A"}}},
		}
	}
	_, _ = p.Update(poll.DataMsg{Resource: gs})
	_ = p.View(120, 40) // 40-row body; no header line in groups

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.cursor, "Ctrl+F walks body-2 from the new cursor (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+B mirrors Ctrl+F symmetrically")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D symmetrically")
}

func TestPage_RenderShowsGroupLabelsAndAlertCount(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Move the cursor off row 0 so it doesn't get wrapped in the
	// row-level Cursor style — that would supersede the per-cell
	// colouring the next test asserts on.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	out := p.View(80, 10)
	require.Contains(t, stripStyle(out), "team=platform")
	require.Contains(t, stripStyle(out), "(2 alerts)")
}

func TestPage_GroupHeaderColoursLabelKVPairs(t *testing.T) {
	t.Parallel()
	styles := loadStyles(t)
	p := New(Options{Styles: styles})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Move cursor off row 0 → row 0 stays plain so per-cell colouring
	// is observable in the rendered string.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	out := p.View(120, 10)
	wantKey := styles.YAML.Key.Render("team")
	wantValue := styles.YAML.Value.Render("platform")
	require.Contains(t, out, wantKey,
		"non-cursor group header must render label name in YAML.Key style")
	require.Contains(t, out, wantValue,
		"non-cursor group header must render label value in YAML.Value style")
}

func TestPage_LeafRowsColourAlertnameAndState(t *testing.T) {
	t.Parallel()
	styles := loadStyles(t)
	p := New(Options{Styles: styles})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // expand row 0
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	// Cursor now sits on the second leaf alert (B). The first leaf
	// ("A") is plain and should carry per-cell colouring.
	out := p.View(120, 10)
	wantName := styles.YAML.Key.Render("A")
	require.Contains(t, out, wantName,
		"non-cursor leaf row must render alertname in YAML.Key style")
}

func TestPage_TenantColumnAppearsOnMultiTenantScope(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.AlertGroup{{
			Labels: map[string]string{"team": "platform"},
			Alerts: []backend.Alert{{Labels: map[string]string{"alertname": "A"}}},
		}},
		Tenant: "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.AlertGroup{{
			Labels: map[string]string{"team": "data"},
			Alerts: []backend.Alert{{Labels: map[string]string{"alertname": "B"}}},
		}},
		Tenant: "staging",
	})

	out := stripStyle(p.View(140, 10))
	require.Contains(t, out, "prod",
		"two in-scope tenants must surface a TENANT prefix on group headers")
	require.Contains(t, out, "staging")
}

func TestPage_TenantColumnHiddenOnSingleTenantScope(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.AlertGroup{{
			Labels: map[string]string{"team": "platform"},
			Alerts: []backend.Alert{{Labels: map[string]string{"alertname": "A"}}},
		}},
		Tenant: "prod",
	})
	out := stripStyle(p.View(140, 10))
	require.NotContains(t, out, "prod ",
		"single-tenant scope hides the TENANT column even though "+
			"the tenant tag is in byTenant")
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

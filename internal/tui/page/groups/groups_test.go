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
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
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

func TestPage_DefaultsToNameAscending(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc(), "alphabetical name read-order is the default")
}

func TestPage_SortByNameOrdersAlphabetically(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	// sampleGroups has team=platform first and team=data second
	// in source order; Name ASC must reorder to data, platform.
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	require.Equal(t, "data", p.flat[0].g.Labels["team"])
	require.Equal(t, "platform", p.flat[1].g.Labels["team"])
}

func TestPage_SortByCountPutsBiggestGroupFirst(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Default: ascending by name → data (1 alert), platform (2 alerts).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	require.Equal(t, sortKeyCount, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(), "Count default is DESC — biggest groups first")
	require.Len(t, p.flat[0].g.Alerts, 2,
		"DESC count puts the platform group (2 alerts) above data (1)")
}

func TestPage_SortBySeverityPutsCriticalGroupFirst(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(), "Severity default is DESC — critical first")
	require.Equal(t, "platform", p.flat[0].g.Labels["team"],
		"platform carries a critical alert; data has none → platform first")
}

func TestPage_SortShortcutTogglesDirection(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc())

	// Same column shortcut flips direction.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.False(t, p.sorter.Asc())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.True(t, p.sorter.Asc())

	// Different column resets to that column's default direction.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	require.Equal(t, sortKeyCount, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(), "Count default is DESC")
}

func TestPage_SortColumnWalk(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, sortKeyCount, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey(), "right walk wraps from Severity back to Name")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey(), "left walk wraps to the rightmost axis")
}

func TestPage_BindingsExposeSortShortcutsForHelpOverlay(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	want := map[string]string{
		"Shift+N": "sort by name",
		"Shift+C": "sort by alert count",
		"Shift+V": "sort by severity",
	}
	got := map[string]string{}
	for _, b := range p.Bindings() {
		if strings.HasPrefix(b.Key, "Shift+") {
			got[b.Key] = b.Description
		}
	}
	for k, desc := range want {
		require.Contains(t, got, k,
			"Bindings() must surface %s so the `?` overlay's HOTKEYS column lists it", k)
		require.Equal(t, desc, got[k],
			"sort description for %s must match the keybindings.md table", k)
	}
}

func TestPage_UserResortKeepsCursorAtRowIndex(t *testing.T) {
	t.Parallel()
	// User-initiated re-sort is k9s-positional: the cursor stays at
	// the same row index, whichever group lands under it becomes
	// the new focus. This pairs with poll/scope/filter recomputes
	// which still follow the focused row by key (see
	// TestPage_DataMsgKeepsCursor* if/when added).
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Default Name ASC: row 0 = data, row 1 = platform. Walk to
	// platform (row 1).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.cursor)
	require.Equal(t, "platform", p.flat[p.cursor].g.Labels["team"])

	// Shift+C → Count DESC. platform (2 alerts) moves to row 0,
	// data to row 1. Cursor must STAY at row 1 (now data), not
	// follow platform.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	require.Equal(t, 1, p.cursor, "cursor stays at row index on user re-sort")
	require.Equal(t, "data", p.flat[p.cursor].g.Labels["team"],
		"the group landing at the held index becomes the new focus")
}

func TestPage_UserResortOnExpandedLeafKeepsRowIndex(t *testing.T) {
	t.Parallel()
	// Same contract on a leaf row: the cursor's *index* survives the
	// re-sort, even when the previously-focused leaf moves elsewhere
	// in the visible row list.
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Walk to platform (row 1), expand, walk to leaf A (row 2),
	// then leaf B (row 3).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	heldIdx := p.cursor

	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	require.Equal(t, heldIdx, p.cursor,
		"cursor stays at the same row index across user re-sort")
}

func TestPage_HeaderRendersActiveSortArrow(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	out := testutil.StripStyle(p.View(120, 10))
	require.Contains(t, out, "NAME ↑",
		"default ASC sort must surface an ↑ arrow next to the active axis label")
	require.Contains(t, out, "COUNT")
	require.Contains(t, out, "SEVERITY")

	// Toggle to DESC — arrow flips.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	out = testutil.StripStyle(p.View(120, 10))
	require.Contains(t, out, "NAME ↓",
		"DESC must surface a ↓ arrow on the same active axis")
}

func TestPage_HeaderRendersForegroundOnly(t *testing.T) {
	t.Parallel()
	// TUI chrome stays on terminal default background — painting
	// palette bg inside the unstyled body frame creates a coloured
	// stripe. Asserts the header line carries no SGR background
	// code.
	p := New(Options{Styles: loadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	headerLine, _, _ := strings.Cut(p.View(120, 10), "\n")
	require.NotContains(t, headerLine, "\x1b[48",
		"header must not paint a palette background — chrome stays on terminal default bg")
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

	// Default Name ASC puts data (1 alert) first, platform (2 alerts)
	// second. Move the cursor to platform so the expand check picks
	// up the two-leaf row count.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, p.expanded[1])
	require.Len(t, p.rows(), 4, "expanded platform group adds 2 leaf rows")

	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, p.expanded[1])
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
	// Default Name ASC: row 0 = data, row 1 = platform. Walk to
	// platform, expand it, then drill the first leaf (A).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // expand platform
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

func TestPage_TitleColdStartShowsLoading(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	require.Contains(t, testutil.StripStyle(p.Title()), "loading groups",
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
	_ = p.View(120, 40) // 40-row body; one line goes to the sort header → 39 row budget

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 19, p.cursor, "Ctrl+D walks half the row budget (39 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 56, p.cursor, "Ctrl+F walks budget-2 from the new cursor (19 + 37)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 19, p.cursor, "Ctrl+B mirrors Ctrl+F symmetrically")
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
	require.Contains(t, testutil.StripStyle(out), "team=platform")
	require.Contains(t, testutil.StripStyle(out), "(2 alerts)")
}

func TestPage_GroupHeaderColoursLabelKVPairs(t *testing.T) {
	t.Parallel()
	styles := loadStyles(t)
	p := New(Options{Styles: styles})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Walk the cursor onto whichever row is NOT the platform group
	// so the platform row is guaranteed plain regardless of which
	// default sort lands platform at row 0 vs row 1. The assertion
	// below requires platform to render through the per-cell style,
	// not the cursor-row wrap.
	for p.flat[p.cursor].g.Labels["team"] == "platform" {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}

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
	// Default Name ASC: row 0 = data, row 1 = platform. Walk to
	// platform, expand it, then advance to leaf B so leaf A stays
	// plain and per-cell colouring is observable.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // cursor → platform
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})   // expand platform
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // cursor → A
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // cursor → B
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

	out := testutil.StripStyle(p.View(140, 10))
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
	out := testutil.StripStyle(p.View(140, 10))
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

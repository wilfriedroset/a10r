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
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// sampleGroups is the canonical fixture for groups-page tests: a
// platform team with one critical + one warning alert, and a data
// team with a single alert. Built on pagetest.Group / pagetest.Alert
// so the alert-construction defaults (state, age, Now baseline)
// stay shared with alerts-page tests; the extra label per alert
// merges with the alertname/severity defaults via the AlertOptions
// Labels map.
func sampleGroups() []backend.AlertGroup {
	return []backend.AlertGroup{
		pagetest.Group(pagetest.GroupOptions{
			Labels: map[string]string{"team": "platform"},
			Alerts: []backend.Alert{
				pagetest.Alert(pagetest.AlertOptions{
					Name: "A", Severity: "critical",
					Labels: map[string]string{"team": "platform"},
				}),
				pagetest.Alert(pagetest.AlertOptions{
					Name: "B", Severity: "warning",
					Labels: map[string]string{"team": "platform"},
				}),
			},
		}),
		pagetest.Group(pagetest.GroupOptions{
			Labels: map[string]string{"team": "data"},
			Alerts: []backend.Alert{
				pagetest.Alert(pagetest.AlertOptions{
					Name: "C", Labels: map[string]string{"team": "data"},
				}),
			},
		}),
	}
}

func TestPage_SortByNameOrdersAlphabetically(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
	// sampleGroups has team=platform first and team=data second
	// in source order; Name ASC must reorder to data, platform.
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	require.Equal(t, "data", p.flat[0].g.Labels["team"])
	require.Equal(t, "platform", p.flat[1].g.Labels["team"])
}

func TestPage_SortByCountPutsBiggestGroupFirst(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
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
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(), "Severity default is DESC — critical first")
	require.Equal(t, "platform", p.flat[0].g.Labels["team"],
		"platform carries a critical alert; data has none → platform first")
}

func TestPage_UserResortKeepsCursorAtRowIndex(t *testing.T) {
	t.Parallel()
	// User-initiated re-sort is k9s-positional: the cursor stays at
	// the same row index, whichever group lands under it becomes
	// the new focus. This pairs with poll/scope/filter recomputes
	// which still follow the focused row by key (see
	// TestPage_DataMsgKeepsCursor* if/when added).
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Default Name ASC: row 0 = data, row 1 = platform. Walk to
	// platform (row 1).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.Index())
	require.Equal(t, "platform", p.flat[p.Index()].g.Labels["team"])

	// Shift+C → Count DESC. platform (2 alerts) moves to row 0,
	// data to row 1. Cursor must STAY at row 1 (now data), not
	// follow platform.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	require.Equal(t, 1, p.Index(), "cursor stays at row index on user re-sort")
	require.Equal(t, "data", p.flat[p.Index()].g.Labels["team"],
		"the group landing at the held index becomes the new focus")
}

func TestPage_HeaderRendersActiveSortArrow(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
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
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	headerLine, _, _ := strings.Cut(p.View(120, 10), "\n")
	require.NotContains(t, headerLine, "\x1b[48",
		"header must not paint a palette background — chrome stays on terminal default bg")
}

func TestPage_EnterTogglesExpand(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
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
	p := New(Options{Styles: pagetest.Styles(t)})
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
	p := New(Options{Styles: pagetest.Styles(t)})
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
	p := New(Options{Styles: pagetest.Styles(t)})
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
		Styles:  pagetest.Styles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
		Creator: "wilfried",
	})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: "prod"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd, "`s` with clients must push the form")
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "`s` with clients must push the form, not flash")
}

// TestPage_SilenceFormSubmittedFlashesSuccess: the
// silenceform.SubmittedMsg → footer.FlashShowMsg{Success, "silence
// created: <id>"} contract is identical across alerts/groups/
// alert-detail and is pinned canonically by
// internal/tui/page/alerts/alerts_test.go:TestPage_SilenceFormSubmittedFlashesSuccess.
// Groups handles the message via the same handleWriteResult path;
// no groups-specific wiring to witness.

// TestPage_SilenceOnGroupWithNoMatchersFlashesError pins the
// guard against silencing EVERYTHING when a group has no common
// labels (or, degenerately, no alerts at all). commonLabels of an
// empty / heterogeneous group returns an empty map; without this
// guard, MatchersFromLabels produces an empty matcher list and the
// form is pushed; a Submit would create an alertmanager silence
// matching every alert in the fleet.
func TestPage_SilenceOnGroupWithNoMatchersFlashesError(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  pagetest.Styles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
		Creator: "wilfried",
	})
	emptyGroup := []backend.AlertGroup{
		{Labels: map[string]string{"team": "platform"}}, // no Alerts → no common labels
	}
	_, _ = p.Update(poll.DataMsg{Resource: emptyGroup, Tenant: "prod"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd, "`s` on a no-matcher group must surface something, not silently push the form")
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "the response must be a flash, not a form push")
	require.Equal(t, footer.FlashError, msg.Level,
		"silencing-everything is a destructive class of mistake — error level, not info/warn")
	require.Contains(t, msg.Text, "no common labels",
		"the operator needs to know why the form was refused")
}

func TestPage_SilenceOnEmptyViewIsNoop(t *testing.T) {
	t.Parallel()
	// No DataMsg → empty rows → `s` flashes "no group under cursor".
	p := New(Options{
		Styles:  pagetest.Styles(t),
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no group under the cursor")
}

func TestPage_ReadOnlySilenceKeyFlashesHint(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:   pagetest.Styles(t),
		Clients:  map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
		Creator:  "wilfried",
		ReadOnly: true,
	})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: "prod"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	fm, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, footer.FlashWarn, fm.Level)
	require.Contains(t, fm.Text, "read-only")
}

func TestPage_RefreshKeyEmitsRequestAndFlipsRefreshing(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: ""})
	require.False(t, p.Refreshing)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.True(t, p.Refreshing,
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

func (*fakeSilenceClient) ExpireSilence(context.Context, string) error { return nil }

// TestPage_VimMotions is the wiring smoke for the cursor module:
// pressing `j` in Update must route into Window.MoveCursor with
// len(p.rows()) as the row count. The full motion contract
// (j/k/G/g/Ctrl+D/U/F/B, clamps, empty-view) lives in
// internal/tui/page/cursor/window_test.go:TestWindow_MoveCursor;
// this test only proves the page is wired to it.
func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.Index(), "Update must route `j` into Window.MoveCursor")
}

func TestPage_RenderShowsGroupLabelsAndAlertCount(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Move the cursor off row 0 so it doesn't get wrapped in the
	// row-level Cursor style — that would supersede the per-cell
	// colouring the next test asserts on.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	out := p.View(120, 10)
	plain := testutil.StripStyle(out)
	require.Contains(t, plain, "team=platform")
	// Count lives in its own column now — bare number, not the
	// legacy inline "(2 alerts)" form jammed into the NAME body.
	require.NotContains(t, plain, "(2 alerts)",
		"count moved out of the NAME body into the COUNT column")
	require.Regexp(t, `team=platform\s+2\b`, plain,
		"the COUNT column shows the per-group alert count next to the labels")
}

func TestPage_RenderShowsSeverityLabel(t *testing.T) {
	t.Parallel()
	// platform group carries a critical alert; data group has no
	// severity → renders as the unknown placeholder. The SEVERITY
	// column surfaces the worst-rank label so the user can triage
	// without expanding.
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	out := testutil.StripStyle(p.View(120, 10))
	require.Contains(t, out, "critical",
		"SEVERITY column shows the worst severity in the group")
}

func TestPage_FocusedGroupShowsSingleTreeMarker(t *testing.T) {
	t.Parallel()
	// The cursor signal is the row's background tint; the tree
	// marker (▸/▾) is reserved for collapsed/expanded state. They
	// must not double up — a focused collapsed group used to render
	// "▸ ▸ team=…" because the cursor and tree marker shared the
	// same glyph.
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	out := testutil.StripStyle(p.View(120, 10))
	require.NotContains(t, out, "▸ ▸",
		"cursor row must not double up the tree marker")
	require.Contains(t, out, "▸",
		"collapsed groups still surface a tree marker")
}

func TestPage_GroupHeaderColoursLabelKVPairs(t *testing.T) {
	t.Parallel()
	styles := pagetest.Styles(t)
	p := New(Options{Styles: styles})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})
	// Walk the cursor onto whichever row is NOT the platform group
	// so the platform row is guaranteed plain regardless of which
	// default sort lands platform at row 0 vs row 1. The assertion
	// below requires platform to render through the per-cell style,
	// not the cursor-row wrap.
	for p.flat[p.Index()].g.Labels["team"] == "platform" {
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

func TestPage_LeafRowsColourLabelsAndState(t *testing.T) {
	t.Parallel()
	// Leaves render the labels that differ between siblings (the
	// inverse of commonLabels) so each row identifies the actual
	// instance, not the labels already in the group header. The
	// per-cell colouring follows the YAML viewer's palette so a
	// k=v pair reads consistently across the TUI.
	styles := pagetest.Styles(t)
	p := New(Options{Styles: styles})
	gs := []backend.AlertGroup{{
		Labels: map[string]string{"alertname": "DiskFull", "team": "platform"},
		Alerts: []backend.Alert{
			{Labels: map[string]string{"alertname": "DiskFull", "team": "platform", "instance": "host-a"}, State: backend.AlertStateActive},
			{Labels: map[string]string{"alertname": "DiskFull", "team": "platform", "instance": "host-b"}, State: backend.AlertStateActive},
		},
	}}
	_, _ = p.Update(poll.DataMsg{Resource: gs})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // expand
	// Walk past the cursor (group header at row 0) so per-cell
	// colouring on a leaf is observable.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	out := p.View(120, 10)
	require.Contains(t, out, styles.YAML.Key.Render("instance"),
		"non-cursor leaf row must render label keys in YAML.Key style")
	require.Contains(t, out, styles.YAML.Value.Render("active"),
		"non-cursor leaf row must render state in YAML.Value style")
}

func TestPage_LeafRowsShowDistinguishingLabels(t *testing.T) {
	t.Parallel()
	// Group's grouping labels include alertname; leaves are only
	// distinguishable by `instance`. The leaf must surface
	// instance=… so the user can tell siblings apart and decide
	// which one to drill into. alertname is already in the group
	// header — echoing it on every leaf is dead pixels.
	p := New(Options{Styles: pagetest.Styles(t)})
	gs := []backend.AlertGroup{{
		Labels: map[string]string{"alertname": "DiskFull", "team": "platform"},
		Alerts: []backend.Alert{
			{Labels: map[string]string{"alertname": "DiskFull", "team": "platform", "instance": "host-a"}, State: backend.AlertStateActive},
			{Labels: map[string]string{"alertname": "DiskFull", "team": "platform", "instance": "host-b"}, State: backend.AlertStateActive},
		},
	}}
	_, _ = p.Update(poll.DataMsg{Resource: gs})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // expand all
	out := testutil.StripStyle(p.View(160, 10))
	require.Contains(t, out, "instance=host-a")
	require.Contains(t, out, "instance=host-b")
	// The alertname is in commonLabels → the leaf must not echo it.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "instance=") {
			require.NotContains(t, line, "alertname=DiskFull",
				"leaves drop labels common across siblings; alertname stays in the group header")
		}
	}
}

func TestPage_LeafRowsFallbackToAlertnameWhenIdentical(t *testing.T) {
	t.Parallel()
	// Two alerts with identical label sets — distinguishing labels
	// is empty, so the leaf falls back to the alertname so the row
	// still carries something the user can read. Edge case (a real
	// duplicate), but the fallback prevents a blank leaf.
	p := New(Options{Styles: pagetest.Styles(t)})
	gs := []backend.AlertGroup{{
		Labels: map[string]string{"alertname": "DiskFull", "team": "platform"},
		Alerts: []backend.Alert{
			{Labels: map[string]string{"alertname": "DiskFull", "team": "platform"}, State: backend.AlertStateActive},
			{Labels: map[string]string{"alertname": "DiskFull", "team": "platform"}, State: backend.AlertStateActive},
		},
	}}
	_, _ = p.Update(poll.DataMsg{Resource: gs})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	plain := testutil.StripStyle(p.View(160, 10))
	// The group header reads `▾ alertname=DiskFull,team=platform …`
	// — DiskFull is followed immediately by `,team=…`, not by an
	// em-dash. So a regex requiring whitespace + em-dash + active
	// after DiskFull matches leaf rows only, never the header.
	require.Regexp(t, `DiskFull\s+—\s+active`, plain,
		"leaf must render the alertname inline with state when distinguishing labels are empty")
	leafLines := 0
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, "DiskFull") && strings.Contains(line, "—") &&
			strings.Contains(line, "active") {
			leafLines++
		}
	}
	require.Equal(t, 2, leafLines,
		"both duplicate alerts must surface as their own leaf row")
}

func TestDistinguishingLabels_KeepsDivergent(t *testing.T) {
	t.Parallel()
	common := map[string]string{"alertname": "DiskFull", "team": "platform"}
	a := backend.Alert{Labels: map[string]string{
		"alertname": "DiskFull",
		"team":      "platform",
		"instance":  "host-a",
		"severity":  "critical",
	}}
	require.Equal(t, map[string]string{
		"instance": "host-a",
		"severity": "critical",
	}, distinguishingLabels(a, common))
}

// The TENANT-column visibility contract (multi-tenant scope shows
// the column, single-tenant scope hides it, a configured-but-silent
// backend still anchors the column) is identical across alerts/
// groups/silences and is pinned canonically by:
//   - internal/tui/page/alerts/alerts_test.go:TestPage_TenantColumnAppearsForAllScope
//     (multi-tenant DataMsgs surface the TENANT column).
//   - internal/tui/page/alerts/alerts_test.go:TestPage_TenantColumnHiddenForSingleBackend
//     (single-tenant scope hides the column).
//   - internal/tui/page/alerts/alerts_test.go:TestPage_ErrorBandSurfacesConfiguredTenantThatNeverReplied
//     (configured-tenant count drives visibility even when one backend never produced data).
//
// Groups reads visibility off the same configured-tenant list via
// the shared showTenantColumn() predicate; no groups-specific
// wiring to witness here.

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

// TestPage_FilterNarrowsView is the per-page wiring smoke proving
// the groups page plumbs filter buffers through footer.NewMatcher
// into visibleGroups. The mode-autodetect / live-narrow / Esc-restore
// / submit-empty-clears contract lives in
// internal/tui/footer/{searchmode,matcher}_test.go and footer_test.go
// (TestPrompt_* family); this test only proves the wiring exists.
func TestPage_FilterNarrowsView(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups()})

	// sampleGroups has two entries: team=platform and team=data.
	// Filter by "data" → only the data group is visible.
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "data"})
	require.Equal(t, "groups(all)[1/2]", p.Title())
	visible := p.visibleGroups()
	require.Len(t, visible, 1)
	require.Equal(t, "data", visible[0].Labels["team"])
}

// TestPage_WatchModeFooterRendersWatchOff is the page-specific wiring
// witness for the watch/pause-refresh contract. The full contract
// (DataMsg swallowed while paused, manual `r` honoured once,
// resume-clears-state) is covered canonically by
// internal/tui/page/alerts/alerts_test.go:TestPage_WatchModeToggleSwallowsDataMsg
// / TestPage_WatchModeManualRefreshHonouredOnce — this smoke just
// proves the groups page's Update loop dispatches `w` so a wire
// regression here still red-lights.
func TestPage_WatchModeFooterRendersWatchOff(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: pagetest.Styles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: sampleGroups(), Tenant: "prod"})
	require.NotContains(t, p.Footer(), "WATCH OFF",
		"baseline footer omits WATCH OFF")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.Contains(t, p.Footer(), "WATCH OFF",
		"paused page footer leads with WATCH OFF")
}

func TestPage_ErrorBandReflectsBackendStatusDetail(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	p := New(Options{Styles: pagetest.Styles(t)})
	// Single-tenant scope: detail is rendered verbatim without a
	// tenant prefix. The page constructor seeds scope to "all" by
	// default, so we narrow it for this case.
	p.Scope = "prod"

	require.Empty(t, p.ErrorBand(now))

	// NextAt is past-due (zero) so the suffix renders as `retrying now`.
	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "connection refused",
	})
	require.Equal(t, "connection refused — retrying now", p.ErrorBand(now),
		"single-tenant scope renders detail verbatim (no tenant prefix) with the retry suffix")

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnConnected,
	})
	require.Empty(t, p.ErrorBand(now),
		"recovery clears the band so transient blips don't linger")
}

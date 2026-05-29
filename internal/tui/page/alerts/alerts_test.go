// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/alert"
	"github.com/wilfriedroset/a10r/internal/tui/page/groupdetail"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// fixedNow is a deterministic clock for the age-column tests; shared
// with the chrome / bench / soak suites in this package.
var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func newPage(t *testing.T) *Page {
	t.Helper()
	return New(Options{
		Styles: pagetest.Styles(t),
		Now:    func() time.Time { return fixedNow },
	})
}

// mkAlert builds an alert with an explicit fingerprint and optional
// extra labels — the aggregate tests need stable fingerprints and
// distinguishing labels across instances of one alertname.
func mkAlert(name, severity string, state backend.AlertState, fp string, age time.Duration, extra map[string]string) backend.Alert {
	a := pagetest.Alert(pagetest.AlertOptions{
		Name: name, Severity: severity, State: state, Now: fixedNow, Age: age, Labels: extra,
	})
	a.Fingerprint = fp
	return a
}

// --- Aggregation -----------------------------------------------------

func TestAggregate_RollsInstancesByAlertname(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", 3*time.Minute, map[string]string{"instance": "a"}),
		mkAlert("HighCPU", "critical", backend.AlertStateSuppressed, "fp2", 9*time.Minute, map[string]string{"instance": "b"}),
		mkAlert("HighCPU", "info", backend.AlertStateActive, "fp3", 1*time.Minute, map[string]string{"instance": "c"}),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp4", 5*time.Minute, map[string]string{"instance": "d"}),
	}})

	require.Len(t, p.groups, 2, "3 HighCPU + 1 DiskFull → 2 groups")

	byName := map[string]alertGroup{}
	for _, g := range p.groups {
		byName[g.alertName] = g
	}
	cpu := byName["HighCPU"]
	require.Equal(t, 3, cpu.count)
	require.Equal(t, 3, cpu.severityRank, "max severity (critical) wins")
	require.Equal(t, fixedNow.Add(-9*time.Minute), cpu.oldestStart, "oldest StartsAt")
	require.Equal(t, 2, cpu.active)
	require.Equal(t, 1, cpu.suppressed)
	require.Equal(t, 0, cpu.unprocessed)
	require.Equal(t, []string{"fp1", "fp2", "fp3"}, fingerprints(cpu.instances), "instances sorted by fingerprint ASC")

	disk := byName["DiskFull"]
	require.Equal(t, 1, disk.count)
}

func TestAggregate_SameAlertnameDistinctTenantsDoNotMerge(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Tenants: []string{"prod", "stg"},
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp-prod", time.Minute, nil),
	}})
	_, _ = p.Update(poll.DataMsg{Tenant: "stg", Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp-stg", time.Minute, nil),
	}})

	require.Len(t, p.groups, 2, "same alertname under two tenants stays two groups")
	tenants := map[string]struct{}{}
	for _, g := range p.groups {
		tenants[g.tenant] = struct{}{}
		require.Equal(t, 1, g.count)
	}
	require.Contains(t, tenants, "prod")
	require.Contains(t, tenants, "stg")
}

func TestAggregate_FilterNarrowsInstancesThenRegroups(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, map[string]string{"instance": "keep"}),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp2", time.Minute, map[string]string{"instance": "drop"}),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp3", time.Minute, map[string]string{"instance": "drop"}),
	}})
	require.Len(t, p.groups, 2)

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "keep"})
	require.Len(t, p.groups, 1, "filter drops the DiskFull group and narrows HighCPU")
	require.Equal(t, "HighCPU", p.groups[0].alertName)
	require.Equal(t, 1, p.groups[0].count, "COUNT reflects survivors only")
}

func TestAggregate_StateFilterReshapesBreakdown(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		mkAlert("HighCPU", "warning", backend.AlertStateSuppressed, "fp2", time.Minute, nil),
	}})
	require.Equal(t, 2, p.groups[0].count)

	// Cycle the state filter to "active" — one instance survives.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'F', Text: "F"})
	require.Equal(t, "active", p.stateFilter)
	require.Len(t, p.groups, 1)
	require.Equal(t, 1, p.groups[0].count)
	require.Equal(t, 1, p.groups[0].active)
	require.Equal(t, 0, p.groups[0].suppressed)
}

func TestAggregate_MissingAlertnameGroupsUnderSyntheticKey(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	noName := backend.Alert{
		Labels:      map[string]string{"severity": "warning"},
		Fingerprint: "fp-x",
		State:       backend.AlertStateActive,
		StartsAt:    fixedNow.Add(-time.Minute),
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{noName}})

	require.Len(t, p.groups, 1)
	require.Empty(t, p.groups[0].alertName)
	require.NotPanics(t, func() { _ = p.View(120, 20) })
	require.Contains(t, testutil.StripStyle(p.View(120, 20)), noAlertNameCell)
}

func fingerprints(in []backend.Alert) []string {
	out := make([]string, len(in))
	for i, a := range in {
		out[i] = a.Fingerprint
	}
	return out
}

// --- Focus / cursor by group key ------------------------------------

func TestFocus_AnchoredByGroupKeyAcrossResortAndPoll(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("Aaa", "info", backend.AlertStateActive, "fp-a", time.Minute, nil),
		mkAlert("Bbb", "critical", backend.AlertStateActive, "fp-b", time.Minute, nil),
		mkAlert("Ccc", "warning", backend.AlertStateActive, "fp-c", time.Minute, nil),
	}})
	// Default sort is severity DESC → Bbb(crit), Ccc(warn), Aaa(info).
	require.Equal(t, "Bbb", p.groups[p.Index()].alertName)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "Ccc", p.groups[p.Index()].alertName)

	// A poll tick that reorders must keep the cursor on the same group
	// (group-key anchoring across data refresh). Ccc shifts position;
	// the cursor follows it.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("Zzz", "critical", backend.AlertStateActive, "fp-z", time.Minute, nil),
		mkAlert("Ccc", "warning", backend.AlertStateActive, "fp-c", time.Minute, nil),
		mkAlert("Aaa", "info", backend.AlertStateActive, "fp-a", time.Minute, nil),
	}})
	require.Equal(t, "Ccc", p.groups[p.Index()].alertName,
		"cursor follows the focused group key across poll refresh")

	// User-initiated re-sort is positional: the cursor stays at the
	// same row index, whatever group lands under it becomes the new
	// focus (mirrors the per-instance behaviour pre-rollup).
	idxBefore := p.Index()
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"})
	require.Equal(t, idxBefore, p.Index(),
		"re-sort keeps the cursor at the same row index")
}

func TestFocus_PreservedThroughZeroResultFilter(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp2", time.Minute, nil),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // move off row 0
	focused := p.focusGroupKey
	require.NotEmpty(t, focused)

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "no-such-alert"})
	require.Empty(t, p.groups)
	require.Equal(t, focused, p.focusGroupKey, "empty view preserves focus for restore")

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: ""})
	require.Equal(t, focused, p.groups[p.Index()].key(), "clearing filter re-anchors")
}

func TestFocus_ClearedWhenGroupResolvesOut(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp2", time.Minute, nil),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // focus the second row
	focused := p.focusGroupKey
	require.NotEmpty(t, focused)
	idxBefore := p.Index()
	require.Positive(t, idxBefore, "cursor parked off row 0")

	// A poll where the focused group's instances are gone from every
	// tenant — knownKey==false, the truly-resolved branch (distinct from
	// the filter-narrowed branch TestFocus_PreservedThroughZeroResultFilter
	// covers). The phantom focus must clear and the cursor must clamp.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
	}})
	require.Len(t, p.groups, 1, "the resolved group is gone")
	require.NotEqual(t, focused, p.focusGroupKey,
		"a truly-resolved focus key clears rather than chasing a phantom")
	require.Less(t, p.Index(), len(p.groups),
		"cursor clamps inside the shrunken view — no stuck index")
}

// --- Title -----------------------------------------------------------

func TestTitle_GroupCountAndFilteredFraction(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, map[string]string{"instance": "keep"}),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp2", time.Minute, map[string]string{"instance": "drop"}),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "fp3", time.Minute, map[string]string{"instance": "drop"}),
	}})
	require.Equal(t, "alerts(all)[2]", p.Title(), "[N] = group count")

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "keep"})
	require.Equal(t, "alerts(all)[1/2]", p.Title(),
		"[viewGroups/totalGroups]; totalGroups ignores the filter")
}

// --- Render ----------------------------------------------------------

func TestRender_CountColumnCarriesArrowOnSingleInstance(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("Solo", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		mkAlert("Multi", "warning", backend.AlertStateActive, "fp2", time.Minute, nil),
		mkAlert("Multi", "warning", backend.AlertStateActive, "fp3", time.Minute, nil),
	}})
	out := testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, "1 →", "single-instance group shows the skip-to-L3 marker")
	// The multi-instance group's count cell is a bare "2" (no arrow).
	require.Regexp(t, `\b2\b`, out)
	require.NotContains(t, out, "2 →")
}

func TestRender_StateBreakdownFullVsCompact(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "f2", time.Minute, nil),
		mkAlert("HighCPU", "warning", backend.AlertStateSuppressed, "f3", time.Minute, nil),
	}})
	g := p.groups[0]

	require.Equal(t, "2 active · 1 suppressed", stateBreakdownPlain(g, stateformat.Full))
	require.Equal(t, "2ac 1su", stateBreakdownPlain(g, stateformat.Compact))

	// unprocessed bucket appears only when non-zero, in fixed order.
	g2 := alertGroup{count: 6, active: 1, suppressed: 2, unprocessed: 3}
	require.Equal(t, "1 active · 2 suppressed · 3 unprocessed", stateBreakdownPlain(g2, stateformat.Full))
	require.Equal(t, "1ac 2su 3un", stateBreakdownPlain(g2, stateformat.Compact))

	// zero buckets drop entirely.
	g3 := alertGroup{count: 4, suppressed: 4}
	require.Equal(t, "4 suppressed", stateBreakdownPlain(g3, stateformat.Full))
}

// TestStateTokenStyle_ActiveIsNeutral pins the active bucket to the
// default foreground: every row is a firing alert, so "active" is the
// baseline, and a green token would falsely read as healthy. Suppressed
// stays dimmed so the two buckets remain visually distinct.
func TestStateTokenStyle_ActiveIsNeutral(t *testing.T) {
	t.Parallel()
	styles := pagetest.Styles(t)
	active := stateTokenStyle(backend.AlertStateActive, styles).Render("54 active")
	require.Equal(t, "54 active", active,
		"the active bucket must render in the default foreground (no colour SGR)")
	suppressed := stateTokenStyle(backend.AlertStateSuppressed, styles).Render("3 suppressed")
	require.NotEqual(t, "3 suppressed", suppressed,
		"the suppressed bucket must be dimmed (styled) so it reads distinct from active")
}

func TestRender_MaxSeverityCell(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "info", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("HighCPU", "critical", backend.AlertStateActive, "f2", time.Minute, nil),
	}})
	out := testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, "critical", "SEVERITY cell shows the max severity label")
	require.NotContains(t, out, "info")
}

func TestRender_DimsOnlyWhenAllSuppressed(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		// mixed group: one active, one suppressed → not dimmed.
		mkAlert("Mixed", "warning", backend.AlertStateActive, "m1", time.Minute, nil),
		mkAlert("Mixed", "warning", backend.AlertStateSuppressed, "m2", time.Minute, nil),
		// all-suppressed group → dimmed.
		mkAlert("AllSup", "warning", backend.AlertStateSuppressed, "s1", time.Minute, nil),
		mkAlert("AllSup", "warning", backend.AlertStateSuppressed, "s2", time.Minute, nil),
	}})
	// sort by name so row positions are deterministic; move cursor off
	// both rows we inspect by parking it elsewhere.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"}) // alertname ASC: AllSup, Mixed
	out := p.View(120, 20)
	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 3)

	// AllSup is row 0 (lines[1]) and is the cursor row — skip it; check
	// Mixed (row 1, lines[2]) is NOT dimmed-only. Then verify the dim
	// predicate directly for clarity.
	require.False(t, p.groups[1].allSuppressed(), "Mixed group is not all-suppressed")
	require.True(t, p.groups[0].allSuppressed(), "AllSup group is all-suppressed")
}

// --- Sorts -----------------------------------------------------------

func TestSort_BySeverityCountAndTieBreak(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("Bravo", "warning", backend.AlertStateActive, "b1", time.Minute, nil),
		mkAlert("Bravo", "warning", backend.AlertStateActive, "b2", time.Minute, nil),
		mkAlert("Alpha", "critical", backend.AlertStateActive, "a1", time.Minute, nil),
	}})

	// Severity DESC default: Alpha (crit) before Bravo (warn).
	require.Equal(t, "Alpha", p.groups[0].alertName)

	// Count sort DESC: Bravo (2) before Alpha (1).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C"})
	require.Equal(t, "Bravo", p.groups[0].alertName)
	require.Equal(t, 2, p.groups[0].count)
}

func TestSort_TotalOrderTieBreakAlertnameThenTenant(t *testing.T) {
	t.Parallel()

	// Two groups with equal severity AND equal count across two tenants
	// and two alertnames: the tie-break must order by alertName then
	// tenant deterministically.
	in := []alertGroup{
		{tenant: "z", alertName: "Same", count: 1, severityRank: 2},
		{tenant: "a", alertName: "Same", count: 1, severityRank: 2},
		{tenant: "m", alertName: "Other", count: 1, severityRank: 2},
	}
	cols := alertSortColumns()
	var sevLess func(a, b *alertGroup) bool
	for _, c := range cols {
		if c.Key == sortKeySeverity {
			sevLess = c.Less
		}
	}
	require.NotNil(t, sevLess)
	// Other < Same by alertName; among the two "Same", tenant a < z.
	require.True(t, sevLess(&in[2], &in[1]), "Other before Same (alertname)")
	require.True(t, sevLess(&in[1], &in[0]), "Same/a before Same/z (tenant)")
}

func TestSort_StateSortDropped(t *testing.T) {
	t.Parallel()

	for _, c := range alertSortColumns() {
		require.NotEqual(t, "state", c.Key, "the state sort column must be dropped")
	}
}

// --- State-format toggle --------------------------------------------

func TestStateFormat_ShiftTEmitsToggleMsg(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'T', Text: "T"})
	require.NotNil(t, cmd)
	_, ok := cmd().(app.StateFormatToggleMsg)
	require.True(t, ok, "Shift+T must emit StateFormatToggleMsg")
}

func TestStateFormat_ChangedMsgFlipsDensity(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, stateformat.Full, p.stateFormat)
	_, _ = p.Update(app.StateFormatChangedMsg{Format: stateformat.Compact})
	require.Equal(t, stateformat.Compact, p.stateFormat)
}

func TestStateFormat_ZeroValueOptionDefaultsFull(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }})
	require.Equal(t, stateformat.Full, p.stateFormat, "zero-value Options opens in Full")
}

// --- Drill -----------------------------------------------------------

func TestEnter_CountOnePushesInstanceDetail(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("Solo", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
	}})
	page := p.buildInstancePage(p.groups[0])
	_, ok := page.(*alert.Page)
	require.True(t, ok, "COUNT==1 drills to the L3 instance-detail page")

	// The keypress still yields a non-flash push Cmd.
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash)
}

func TestEnter_CountManyPushesGroupDetail(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("Multi", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		mkAlert("Multi", "warning", backend.AlertStateActive, "fp2", time.Minute, nil),
	}})
	page := p.buildGroupPage(p.groups[0])
	_, ok := page.(*groupdetail.Page)
	require.True(t, ok, "COUNT>1 drills to the L2 group-detail page")

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash)
}

func TestEnter_EmptyViewFlashes(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "no alert")
}

// --- Silence-all (cursor) -------------------------------------------

func TestSilenceAll_MatchersAreAlertnameOnly(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]backend.Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
		alertnameMatcher("HighCPU"),
		"silence-all prefills alertname=X alone, not the full label set")
}

func TestSilenceAll_ConfirmGateFiresOnlyForCountGreaterThanOne(t *testing.T) {
	t.Parallel()

	clients := map[string]silenceform.Client{"prod": &fakeSilenceClient{}}

	t.Run("count==1 pushes form directly", func(t *testing.T) {
		t.Parallel()
		p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }, Clients: clients})
		_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
			mkAlert("Solo", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
		}})
		_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
		require.NotNil(t, cmd)
		_, isFlash := cmd().(footer.FlashShowMsg)
		require.False(t, isFlash, "count==1 pushes the form, not a flash")
		require.Equal(t, pendingSilenceAll{}, p.pendingSilenceAll,
			"count==1 pushes the form directly, clearing pending (no confirm gate)")
	})

	t.Run("count>1 opens confirm modal", func(t *testing.T) {
		t.Parallel()
		p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }, Clients: clients})
		_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
			mkAlert("Multi", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
			mkAlert("Multi", "warning", backend.AlertStateActive, "fp2", time.Minute, nil),
		}})
		_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
		require.NotNil(t, cmd)
		_, isFlash := cmd().(footer.FlashShowMsg)
		require.False(t, isFlash, "count>1 opens a confirm modal, not a flash")
		require.Equal(t, "Multi", p.pendingSilenceAll.alertName,
			"count>1 holds the pending target awaiting the confirm result")

		// Yes pushes the prefilled form.
		_, cmd = p.Update(modal.ConfirmResultMsg{Yes: true})
		require.NotNil(t, cmd)
		_, isFlashAfter := cmd().(footer.FlashShowMsg)
		require.False(t, isFlashAfter)
	})
}

func TestSilenceAll_ScopeNoteWording(t *testing.T) {
	t.Parallel()

	g := alertGroup{tenant: "prod", alertName: "HighCPU", count: 3}

	t.Run("no active filter", func(t *testing.T) {
		t.Parallel()
		p := newPage(t)
		require.Equal(t, "Silencing ALL instances of alertname=HighCPU", p.silenceAllScopeNote(g))
	})

	t.Run("substring filter active", func(t *testing.T) {
		t.Parallel()
		p := newPage(t)
		p.Filter = "host"
		require.Equal(t,
			"Silencing ALL instances of alertname=HighCPU — the active filter (filter host) is NOT applied",
			p.silenceAllScopeNote(g))
	})

	t.Run("both filters active", func(t *testing.T) {
		t.Parallel()
		p := newPage(t)
		p.Filter = "host"
		p.stateFilter = "active"
		require.Equal(t,
			"Silencing ALL instances of alertname=HighCPU — the active filter (filter host, state active) is NOT applied",
			p.silenceAllScopeNote(g))
	})
}

func TestSilenceAll_NoWriteableBackendFlashes(t *testing.T) {
	t.Parallel()

	p := newPage(t) // no Clients
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
	}})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend")
}

// --- Bulk silence-all (marks) ---------------------------------------

func TestBulkSilenceAll_FansOutOneAlertnameSilencePerMarkedGroup(t *testing.T) {
	t.Parallel()

	client := &fakeSilenceClient{}
	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]silenceform.Client{"prod": client},
		Creator: "wilfried",
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "c1", time.Minute, map[string]string{"instance": "a"}),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "c2", time.Minute, map[string]string{"instance": "b"}),
		mkAlert("DiskFull", "warning", backend.AlertStateActive, "d1", time.Minute, nil),
	}})

	// Mark both groups (sort by name for deterministic cursor walk).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"}) // DiskFull, HighCPU
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // mark DiskFull
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // mark HighCPU
	require.Len(t, p.marks, 2)

	targets, tenants := p.resolveBulkSilenceTargets()
	require.Len(t, targets, 2, "one target per marked group")
	require.Equal(t, []string{"prod"}, tenants)
	for _, tgt := range targets {
		require.Len(t, tgt.Matchers, 1)
		require.Equal(t, "alertname", tgt.Matchers[0].Name)
		require.Equal(t, tgt.AlertName, tgt.Matchers[0].Value)
	}

	// Drive the fanout: confirm (2 marks) → bulk form → submit.
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})
	submitCmd := p.handleBulkSilenceSubmit(silenceform.BulkSubmittedMsg{
		StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour), Creator: "wilfried", Comment: "maint",
	})
	require.NotNil(t, submitCmd)
	done := submitCmd().(bulkop.DoneMsg[string])
	_ = p.handleBulkSilenceDone(done)

	require.Equal(t, 2, client.callCount(), "two CreateSilence calls, one per group")
	for _, spec := range client.callsCopy() {
		require.Len(t, spec.Matchers, 1)
		require.Equal(t, "alertname", spec.Matchers[0].Name)
	}
	require.Empty(t, p.marks, "successful targets drop their marks")
}

func TestBulkSilenceAll_OneMarkPushesFormDirectly(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "c1", time.Minute, nil),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "one mark pushes the bulk form directly, no flash")
	targets, _ := p.resolveBulkSilenceTargets()
	require.Len(t, targets, 1)
}

func TestBulkSilenceAll_RetainsMarksOnFailureAndFlashes(t *testing.T) {
	t.Parallel()

	markBoth := func(t *testing.T, p *Page) {
		t.Helper()
		_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
			mkAlert("HighCPU", "warning", backend.AlertStateActive, "c1", time.Minute, nil),
			mkAlert("DiskFull", "warning", backend.AlertStateActive, "d1", time.Minute, nil),
		}})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"}) // DiskFull, HighCPU
		_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // mark DiskFull
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // mark HighCPU
		require.Len(t, p.marks, 2)
	}

	drive := func(t *testing.T, p *Page) tea.Cmd {
		t.Helper()
		// confirm (2 marks) → bulk form → submit → DoneMsg → apply.
		_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
		require.NotNil(t, cmd)
		_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})
		submitCmd := p.handleBulkSilenceSubmit(silenceform.BulkSubmittedMsg{
			StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour), Creator: "wilfried", Comment: "maint",
		})
		require.NotNil(t, submitCmd)
		done := submitCmd().(bulkop.DoneMsg[string])
		require.Len(t, done.Results, 2)
		return p.handleBulkSilenceDone(done)
	}

	t.Run("partial failure keeps the failed mark, drops the success", func(t *testing.T) {
		t.Parallel()
		client := &fakeSilenceClient{failOn: map[string]bool{"HighCPU": true}}
		p := New(Options{
			Styles:  pagetest.Styles(t),
			Now:     func() time.Time { return fixedNow },
			Clients: map[string]silenceform.Client{"prod": client},
		})
		markBoth(t, p)
		diskKey := "prod\x00DiskFull"
		cpuKey := "prod\x00HighCPU"

		flash := drive(t, p)
		require.Len(t, p.marks, 1, "only the failed target keeps its mark")
		_, diskMarked := p.marks[diskKey]
		_, cpuMarked := p.marks[cpuKey]
		require.False(t, diskMarked, "succeeded group drops its mark")
		require.True(t, cpuMarked, "failed group keeps its mark so the next `s` retries it")

		require.NotNil(t, flash)
		msg := flash().(footer.FlashShowMsg)
		require.Equal(t, footer.FlashWarn, msg.Level)
		require.Equal(t, "silenced 1 of 2 — 1 failed", msg.Text)
	})

	t.Run("total failure keeps every mark", func(t *testing.T) {
		t.Parallel()
		client := &fakeSilenceClient{wantErr: errors.New("backend down")}
		p := New(Options{
			Styles:  pagetest.Styles(t),
			Now:     func() time.Time { return fixedNow },
			Clients: map[string]silenceform.Client{"prod": client},
		})
		markBoth(t, p)

		flash := drive(t, p)
		require.Len(t, p.marks, 2, "every target keeps its mark on total failure")

		require.NotNil(t, flash)
		msg := flash().(footer.FlashShowMsg)
		require.Equal(t, footer.FlashError, msg.Level)
		require.Equal(t, "silence failed for 2 alerts", msg.Text)
	})
}

func TestSilenceForm_CancelledMsgDropsBothPendingStructs(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// Seed BOTH pending structs to prove the cancel clears the pair —
	// the single-cursor and ≥2-marks paths are distinct, and a stale
	// pending from one must never be consumable by the other.
	p.pendingSilenceAll = pendingSilenceAll{tenant: "prod", alertName: "HighCPU"}
	p.pendingBulkSilence = pendingBulkSilence{
		targets: []bulkSilenceTarget{{Key: "prod\x00DiskFull", Tenant: "prod", AlertName: "DiskFull"}},
		tenants: []string{"prod"},
	}

	_, _ = p.Update(silenceform.CancelledMsg{})

	require.Equal(t, pendingSilenceAll{}, p.pendingSilenceAll,
		"CancelledMsg clears the single-cursor pending")
	require.Empty(t, p.pendingBulkSilence.targets,
		"CancelledMsg clears the bulk pending too")
}

func TestPage_CloseCancelsInFlightBulkFanout(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	cancelled := false
	p.cancelBulk = func() { cancelled = true }

	require.Nil(t, p.Close())
	require.True(t, cancelled, "Close cancels the in-flight bulk fanout")
	require.Nil(t, p.cancelBulk, "Close clears the cancel func after invoking it")
}

// --- Marks keyed by group key ---------------------------------------

func TestMarks_KeyedByGroupKey(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "c1", time.Minute, nil),
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "c2", time.Minute, nil),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Len(t, p.marks, 1)
	_, marked := p.marks[p.groups[0].key()]
	require.True(t, marked, "mark is keyed by group key, not fingerprint")
	require.Contains(t, p.HeaderContent(), "marked:1")
}

// --- Read-only -------------------------------------------------------

func TestReadOnly_StripsSilenceBinding(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }, ReadOnly: true})
	for _, b := range p.Bindings() {
		require.NotEqual(t, "s", b.Key, "read-only mode hides the silence binding")
	}
}

func TestReadOnly_SilenceKeyFlashesHint(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }, ReadOnly: true})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "fp1", time.Minute, nil),
	}})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "read-only")
}

// --- Bindings --------------------------------------------------------

func TestBindings_ExposeCountSortAndStateFormat(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	var hasCount, hasStateFormat bool
	for _, b := range p.Bindings() {
		if strings.Contains(strings.ToLower(b.Description), "count") {
			hasCount = true
		}
		if b.Key == "Shift+T" {
			hasStateFormat = true
		}
	}
	require.True(t, hasCount, "count sort surfaces in bindings")
	require.True(t, hasStateFormat, "Shift+T state-format binding surfaces")
}

// --- Preserved infra -------------------------------------------------

func TestInfra_DropsDataMsgFromUnknownTenant(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Tenants: []string{"prod"},
	})
	p.SetScope("prod")
	_, _ = p.Update(poll.DataMsg{Tenant: "stg", Resource: []backend.Alert{
		mkAlert("Ghost", "warning", backend.AlertStateActive, "g1", time.Minute, nil),
	}})
	require.Empty(t, p.groups, "out-of-scope tenant data does not appear")
}

func TestInfra_TenantColumnAppearsForMultiBackendScope(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Scope:   "all",
		Tenants: []string{"prod", "stg"},
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "p1", time.Minute, nil),
	}})
	out := testutil.StripStyle(p.View(140, 20))
	require.Contains(t, out, "TENANT", "multi-backend scope shows the TENANT column")
	require.Contains(t, out, "prod")
}

func TestInfra_TenantColumnHiddenForSingleBackend(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Tenants: []string{"prod"},
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "p1", time.Minute, nil),
	}})
	out := testutil.StripStyle(p.View(140, 20))
	require.NotContains(t, out, "TENANT")
}

func TestInfra_RefreshKeyEmitsRequestAndFlipsRefreshing(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{}, Tenant: ""})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.NotNil(t, cmd, "r must produce a refresh request Cmd")
	require.True(t, strings.HasSuffix(p.Title(), " loading alerts…"),
		"r re-enters the loading affordance")
}

func TestInfra_WatchModeSwallowsDataMsg(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{}, Tenant: ""})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.Contains(t, p.Footer(), "WATCH OFF")
}

func TestInfra_ErrorBandReflectsBackendStatus(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Empty(t, p.ErrorBand(fixedNow))
	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "connection refused",
	})
	require.Equal(t, "connection refused — retrying now", p.ErrorBand(fixedNow),
		"single-tenant scope renders the detail verbatim with the retry suffix")
}

func TestInfra_FilterPromptWiresThroughPipeline(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("DiskSpace", "warning", backend.AlertStateActive, "f2", time.Minute, nil),
	}})
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "disk"})
	require.Len(t, p.groups, 1)
	require.Equal(t, "DiskSpace", p.groups[0].alertName)
}

func TestInfra_InitialFilterPreseeds(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:        pagetest.Styles(t),
		Now:           func() time.Time { return fixedNow },
		InitialFilter: "disk",
	})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("DiskSpace", "warning", backend.AlertStateActive, "f2", time.Minute, nil),
	}})
	require.Len(t, p.groups, 1)
	require.Equal(t, "DiskSpace", p.groups[0].alertName)
}

func TestInfra_InitialStateFilterPreseeds(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:             pagetest.Styles(t),
		Now:                func() time.Time { return fixedNow },
		InitialStateFilter: "suppressed",
	})
	require.Equal(t, "suppressed", p.stateFilter, "constructor seeds the state filter")
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "warning", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("DiskFull", "warning", backend.AlertStateSuppressed, "f2", time.Minute, nil),
	}})
	require.Len(t, p.groups, 1, "page opens on the suppressed-only view")
	require.Equal(t, "DiskFull", p.groups[0].alertName)
	require.Contains(t, p.HeaderContent(), "state:suppressed")
}

func TestInfra_TimeFormatToggleSwitchesAgeColumn(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, timerender.Relative, p.timeFormat)
	_, _ = p.Update(app.TimeFormatChangedMsg{Format: timerender.Absolute})
	require.Equal(t, timerender.Absolute, p.timeFormat)
}

// --- shared test client ---------------------------------------------

// fakeSilenceClient extends the package-shared testutil stub with the
// tracing the bulk-silence fanout tests need: every spec captured under
// a mutex plus optional per-alertname failure injection.
type fakeSilenceClient struct {
	testutil.FakeSilenceClient
	mu      sync.Mutex
	calls   []backend.SilenceSpec
	failOn  map[string]bool
	wantErr error
}

func (f *fakeSilenceClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	failOn := f.failOn
	wantErr := f.wantErr
	f.mu.Unlock()

	if wantErr != nil {
		return "", wantErr
	}
	for _, m := range spec.Matchers {
		if m.Name == "alertname" && failOn[m.Value] {
			return "", fmt.Errorf("seeded failure for %s", m.Value)
		}
	}
	return "fake-silence-id", nil
}

func (f *fakeSilenceClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSilenceClient) callsCopy() []backend.SilenceSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]backend.SilenceSpec, len(f.calls))
	copy(out, f.calls)
	return out
}

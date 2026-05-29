// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"maps"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

const (
	tenant    = "prod"
	alertName = "HighLatency"
	webInst0  = "web-0"
	webInst1  = "web-1"
	webInst2  = "web-2"
	podKey    = "pod"
	webFilter = "web"
	silID2    = "sil-2"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

// instance builds a backend.Alert for AlertName under the test
// tenant, sharing the cluster=ops common label, with the supplied
// distinguishing labels and a unique fingerprint.
func instance(fp, sev string, state backend.AlertState, extra map[string]string) backend.Alert {
	labels := map[string]string{"cluster": "ops"}
	maps.Copy(labels, extra)
	a := pagetest.Alert(pagetest.AlertOptions{
		Name:     alertName,
		Severity: sev,
		State:    state,
		Labels:   labels,
	})
	a.Fingerprint = fp
	return a
}

func newPage(t *testing.T, instances ...backend.Alert) *Page {
	t.Helper()
	return New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Instances: instances,
	})
}

// data wraps a DataMsg for the test tenant so KnownTenant passes.
func data(alerts ...backend.Alert) poll.DataMsg {
	return poll.DataMsg{Tenant: tenant, ResourceLabel: "alerts", Resource: alerts}
}

func TestNew_SeedInstancesRenderWithDistinguishingLabelsInstanceFirst(t *testing.T) {
	t.Parallel()
	// Two instances sharing cluster=ops (common) but differing on
	// instance + pod (distinguishing). With one instance every label
	// would be "common", so the distinguishing set needs a sibling.
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1, podKey: "p1"}),
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst2, podKey: "p2"}),
	)
	out := testutil.StripStyle(p.View(120, 20))
	// Common label (cluster) does NOT appear on the row; only the
	// distinguishing labels do, instance pinned first.
	rowLines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(rowLines), 3)
	var row string
	for _, l := range rowLines {
		if strings.Contains(l, "instance=web-1") {
			row = l
			break
		}
	}
	require.NotEmpty(t, row, "expected a row carrying instance=web-1")
	require.Contains(t, row, "instance=web-1")
	require.Contains(t, row, "pod=p1")
	require.Less(t, strings.Index(row, "instance=web-1"), strings.Index(row, "pod=p1"),
		"the instance label must be pinned first")
	require.NotContains(t, row, "cluster=ops",
		"common labels must not repeat on the instance row")
}

func TestCommonStrip_ShownByDefaultHiddenAfterShiftC(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst2}),
	)
	out := testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, "common:")
	require.Contains(t, out, "cluster=ops")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	out = testutil.StripStyle(p.View(120, 20))
	require.NotContains(t, out, "common:",
		"Shift+C must collapse the common-labels strip")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	out = testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, "common:", "Shift+C must toggle the strip back on")
}

func TestCommonStrip_OmittedWhenNoSharedLabelsBeyondAlertname(t *testing.T) {
	t.Parallel()
	// Two instances that diverge on cluster and severity: the only
	// remaining common label is alertname, which the strip drops, so
	// the strip is omitted entirely.
	a := instance("fp-1", "critical", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1})
	a.Labels["cluster"] = "a"
	b := instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst2})
	b.Labels["cluster"] = "b"
	p := newPage(t, a, b)
	out := testutil.StripStyle(p.View(120, 20))
	require.NotContains(t, out, "common:",
		"the strip must be omitted when nothing beyond alertname is shared")
}

func TestPoll_AddsAndRemovesInstanceCursorAnchoredByFingerprint(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst2}),
	)
	// Anchor cursor on fp-2 (instance sort default is severity, but
	// both are warning so fingerprint tie-break orders fp-1, fp-2).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "fp-2", p.view[p.Index()].a.Fingerprint)

	// Live poll adds fp-0 (sorts before by instance label) and keeps
	// fp-1/fp-2. Cursor must stay on fp-2.
	_, _ = p.Update(data(
		instance("fp-0", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst0}),
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst2}),
	))
	require.Equal(t, "fp-2", p.view[p.Index()].a.Fingerprint,
		"cursor must stay anchored by fingerprint after an add")

	// Remove fp-2; cursor clamps onto a remaining row.
	_, _ = p.Update(data(
		instance("fp-0", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst0}),
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	))
	require.Len(t, p.view, 2)
	require.NotEqual(t, "fp-2", p.view[p.Index()].a.Fingerprint)
}

func TestPoll_IgnoresAlertsForOtherAlertnames(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	other := pagetest.Alert(pagetest.AlertOptions{Name: "OtherAlert", Severity: "warning", State: backend.AlertStateActive})
	other.Fingerprint = "fp-other"
	mine := instance("fp-mine", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1})
	_, _ = p.Update(data(other, mine))
	require.Len(t, p.instances, 1)
	require.Equal(t, "fp-mine", p.instances[0].Fingerprint)
}

func TestFilter_NarrowsView(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: dbInst}),
	)
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: webFilter})
	require.Len(t, p.view, 1)
	require.Equal(t, "fp-1", p.view[0].a.Fingerprint)
	require.Contains(t, p.HeaderContent(), "filter:web")
}

func TestFilter_LabelMatcherSelectsByLabel(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{"cluster_id": "99"}),
		// fp-2 has the value "99" on a different label (port); a label
		// matcher on cluster_id must not keep it, where a substring would.
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{"cluster_id": "98", "port": "99"}),
	)
	require.Len(t, p.view, 2)

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "cluster_id=99"})
	require.Len(t, p.view, 1)
	require.Equal(t, "fp-1", p.view[0].a.Fingerprint,
		"label matcher keeps only the cluster_id=99 instance")
}

func TestSort_FingerprintTieBreakIsDeterministic(t *testing.T) {
	t.Parallel()
	// All identical severity + identical instance label so only the
	// fingerprint tie-break decides order. Feeding in reverse must
	// still sort fp-a, fp-b, fp-c.
	mk := func(fp string) backend.Alert {
		return instance(fp, "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: "same"})
	}
	p := newPage(t, mk("fp-c"), mk("fp-a"), mk("fp-b"))
	require.Equal(t, []string{"fp-a", "fp-b", "fp-c"},
		[]string{p.view[0].a.Fingerprint, p.view[1].a.Fingerprint, p.view[2].a.Fingerprint})
}

func TestSort_SeverityDescThenFingerprint(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-w", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: "w"}),
		instance("fp-c2", "critical", backend.AlertStateActive, map[string]string{sortKeyInstance: "c2"}),
		instance("fp-c1", "critical", backend.AlertStateActive, map[string]string{sortKeyInstance: "c1"}),
	)
	// Severity DESC default: criticals first, tie-broken by fingerprint.
	require.Equal(t, "fp-c1", p.view[0].a.Fingerprint)
	require.Equal(t, "fp-c2", p.view[1].a.Fingerprint)
	require.Equal(t, "fp-w", p.view[2].a.Fingerprint)
}

func TestEnter_EmptyViewFlashes(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "no instance")
}

func TestResolveToEmpty_ShowsAlertResolvedAndDoesNotPop(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	// All instances resolve away.
	_, cmd := p.Update(data())
	require.Nil(t, cmd, "resolve-to-empty must NOT emit a pop/any Cmd")
	out := testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, "alert resolved")
}

func TestS_ZeroSilencedByFlashes(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S", Mod: tea.ModShift})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "no silences attached")
}

func TestS_UnionPushesSilencesWithRestrictIDs(t *testing.T) {
	t.Parallel()
	a := instance("fp-1", "warning", backend.AlertStateSuppressed, map[string]string{sortKeyInstance: webInst1})
	a.SilencedBy = []string{"sil-1", silID2}
	b := instance("fp-2", "warning", backend.AlertStateSuppressed, map[string]string{sortKeyInstance: webInst2})
	b.SilencedBy = []string{silID2, "sil-3"}
	p := newPage(t, a, b)

	require.Equal(t, []string{"sil-1", silID2, "sil-3"}, p.silencedByUnion(),
		"union must dedup and preserve first-seen order")

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'S', Text: "S", Mod: tea.ModShift})
	require.NotNil(t, cmd)
	// Running the push Cmd must not flash (it pushes a page instead).
	if _, isFlash := cmd().(footer.FlashShowMsg); isFlash {
		t.Fatal("S with a non-empty union must push, not flash")
	}
}

func TestState_FullVsCompactTokenRendering(t *testing.T) {
	t.Parallel()
	require.Equal(t, "active", stateToken(backend.AlertStateActive, stateformat.Full))
	require.Equal(t, "suppressed", stateToken(backend.AlertStateSuppressed, stateformat.Full))
	require.Equal(t, "ac", stateToken(backend.AlertStateActive, stateformat.Compact))
	require.Equal(t, "su", stateToken(backend.AlertStateSuppressed, stateformat.Compact))
	require.Equal(t, "un", stateToken(backend.AlertStateUnprocessed, stateformat.Compact))

	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateSuppressed, map[string]string{sortKeyInstance: webInst1}),
	)
	out := testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, "suppressed")

	p.SetStateFormat(stateformat.Compact)
	out = testutil.StripStyle(p.View(120, 20))
	require.Contains(t, out, " su ")
	require.NotContains(t, out, "suppressed")
}

func TestShiftT_EmitsStateFormatToggle(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'T', Text: "T", Mod: tea.ModShift})
	require.NotNil(t, cmd)
	_, ok := cmd().(app.StateFormatToggleMsg)
	require.True(t, ok, "Shift+T must emit a StateFormatToggleMsg")
}

func TestSortHotkeys_InstanceAndAgeReachable_SeverityViaWalk(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
	)
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey(), "severity is the default column")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.Equal(t, sortKeyInstance, p.sorter.ActiveKey(), "Shift+N sorts by instance label")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'A', Text: "A", Mod: tea.ModShift})
	require.Equal(t, sortKeyAge, p.sorter.ActiveKey(), "Shift+A sorts by age")

	// `S` must NOT be intercepted by the sorter (ADR 0038: S = open
	// silences). Walking back to severity uses h/l instead.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
}

func TestBindings_AdvertiseInstanceAndAgeSortOnly(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	keys := map[string]bool{}
	for _, b := range p.Bindings() {
		keys[b.Key] = true
	}
	require.True(t, keys["Shift+N"], "instance-label sort must be advertised")
	require.True(t, keys["Shift+A"], "age sort must be advertised")
	require.True(t, keys["/"], "substring filter must be advertised")
	require.True(t, keys["Shift+F"], "state filter must be advertised (handleAction implements it)")
	require.False(t, keys["Shift+S"],
		"severity sort must NOT advertise Shift+S — it collides with the S open-silences verb")
	require.True(t, keys["S"], "S (open silences) must be advertised")
	require.True(t, keys["Shift+C"], "common-labels toggle must be advertised")
}

func TestSetStateFormat_HookAppliesBroadcast(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, _ = p.Update(app.StateFormatChangedMsg{Format: stateformat.Compact})
	require.Equal(t, stateformat.Compact, p.stateFormat)
}

func TestReadOnly_StripsDangerousBindings(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		ReadOnly:  true,
	})
	keys := map[string]bool{}
	for _, b := range p.Bindings() {
		keys[b.Key] = true
	}
	require.False(t, keys["s"], "read-only must strip the Dangerous silence binding")
	require.True(t, keys["S"], "S (open silences) is not Dangerous and must remain")
}

func TestReadOnly_SilenceKeyFlashesHint(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		ReadOnly:  true,
		Instances: []backend.Alert{instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1})},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "read-only")
}

func TestTitle_CountsAndFilteredForm(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	// A poll clears the cold-start loading affordance so Title reads
	// the count form rather than "loading…".
	_, _ = p.Update(data(
		instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: dbInst}),
	))
	require.Equal(t, "HighLatency(prod)[2]", p.Title())

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: webFilter})
	require.Equal(t, "HighLatency(prod)[1/2]", p.Title())
}

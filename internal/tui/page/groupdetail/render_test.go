// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Shared literals for the truncation assertions, hoisted so the
// repeated short value and the discriminating tails do not trip
// goconst.
const (
	dbInst   = "db-1"
	tail0042 = "0042"
	tail0117 = "0117"
)

// rowContaining returns the first rendered line containing sub.
func rowContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no rendered line contains %q\n%s", sub, out)
	return ""
}

// TestRender_SiblingInstancesStayDistinguishableAt80 asserts two
// instances sharing a long prefix but differing in the tail render
// with different visible cells at width 80 — the discriminating tail
// must survive the truncation (middle-out ellipsis).
func TestRender_SiblingInstancesStayDistinguishableAt80(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: "node-pool-region-eu-west-1a-zone-0042"}),
		instance("fp-2", "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: "node-pool-region-eu-west-1b-zone-0117"}),
	)
	out := testutil.StripStyle(p.View(80, 24))
	a := rowContaining(t, out, tail0042)
	b := rowContaining(t, out, tail0117)
	require.NotEqual(t, strings.TrimRight(a, " "), strings.TrimRight(b, " "),
		"siblings must render distinguishably")
	require.Contains(t, a, tail0042, "discriminating tail must survive truncation")
	require.Contains(t, b, tail0117, "discriminating tail must survive truncation")
}

// TestRender_FiringInstanceLabelsColored asserts a firing (active)
// non-cursor instance renders its distinguishing labels in the YAML
// palette so an actionable instance reads
// in colour while the cursor row keeps its row-level highlight.
func TestRender_FiringInstanceLabelsColored(t *testing.T) {
	t.Parallel()
	styles := pagetest.Styles(t)
	p := New(Options{
		Styles: styles, Now: func() time.Time { return fixedNow },
		Tenant: tenant, AlertName: alertName,
		Instances: []backend.Alert{
			// Cursor lands on row 0 (fp-0); fp-1 is the firing
			// non-cursor row whose labels must be coloured.
			instance("fp-0", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst0}),
			instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1}),
		},
		Session: testutil.Session(),
	})
	raw := p.View(120, 20)
	// Both the name and the value are coloured (the full k=v tokenizer),
	// not just the key. web-1 is a distinguishing value, so YAML.Value
	// on it can only come from a coloured row, never the common strip.
	require.Contains(t, raw, styles.YAML.Key.Render(sortKeyInstance),
		"a firing non-cursor instance must render its label key in the YAML palette")
	require.Contains(t, raw, styles.YAML.Value.Render(webInst1),
		"the label value must be coloured too, not just the key")
	// Colouring must not change the visible content (width invariant).
	require.Contains(t, testutil.StripStyle(raw), "instance=web-1")
}

// TestRender_NonActiveInstancesDimmedNotColored asserts suppressed and
// unprocessed instances recede — their rows dim and their labels are
// NOT given the firing colour, so the firing instances stand out.
func TestRender_NonActiveInstancesDimmedNotColored(t *testing.T) {
	t.Parallel()
	styles := pagetest.Styles(t)
	p := New(Options{
		Styles: styles, Now: func() time.Time { return fixedNow },
		Tenant: tenant, AlertName: alertName,
		Instances: []backend.Alert{
			// Cursor on fp-0 (active); the two non-cursor rows are
			// suppressed / unprocessed, so nothing gets the firing colour.
			instance("fp-0", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst0}),
			instance("fp-1", "warning", backend.AlertStateSuppressed, map[string]string{sortKeyInstance: webInst1}),
			instance("fp-2", "warning", backend.AlertStateUnprocessed, map[string]string{sortKeyInstance: webInst2}),
		},
		Session: testutil.Session(),
	})
	raw := p.View(120, 20)
	require.NotContains(t, raw, styles.YAML.Key.Render(sortKeyInstance),
		"non-active instances must not get the firing label colour")
	out := testutil.StripStyle(raw)
	require.Contains(t, out, "instance=web-1")
	require.Contains(t, out, "instance=web-2")

	// Both non-active rows are dimmed: the DimmedFg opening SGR appears.
	dimOpen, _, _ := strings.Cut(styles.Table.DimmedFg.Render("x"), "x")
	require.NotEmpty(t, dimOpen, "test theme must give DimmedFg a real SGR")
	// Both the suppressed (web-1) AND the unprocessed (web-2) row must
	// dim — unprocessed dimming is the new behavior this change adds.
	rawLine := func(sub string) string {
		for l := range strings.SplitSeq(raw, "\n") {
			if strings.Contains(testutil.StripStyle(l), sub) {
				return l
			}
		}
		return ""
	}
	require.Contains(t, rawLine("instance=web-1"), dimOpen, "suppressed row must be dimmed")
	require.Contains(t, rawLine("instance=web-2"), dimOpen, "unprocessed row must be dimmed")
}

// TestRender_SeverityExcludedFromDistinguishingLabels asserts the
// `severity=` token never appears in the distinguishing-labels cell
// even when severities diverge across instances — it lives in the
// dedicated SEVERITY column instead.
func TestRender_SeverityExcludedFromDistinguishingLabels(t *testing.T) {
	t.Parallel()
	p := newPage(t,
		instance("fp-1", "critical", backend.AlertStateActive,
			map[string]string{sortKeyInstance: webInst1}),
		instance("fp-2", "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: webInst2}),
	)
	out := testutil.StripStyle(p.View(200, 24))
	require.NotContains(t, out, "severity=",
		"severity must not appear in the distinguishing-labels cell")
	// SEVERITY column still carries the values.
	require.Contains(t, out, "critical")
	require.Contains(t, out, "warning")
}

// TestDistinguishingSummary_DropsSeverityPinsInstance is the unit-level
// guard on the summary builder.
func TestDistinguishingSummary_DropsSeverityPinsInstance(t *testing.T) {
	t.Parallel()
	a := backend.Alert{Labels: map[string]string{
		sortKeyInstance: webInst1,
		"severity":      "critical",
		podKey:          "p1",
	}}
	common := map[string]string{}
	got := distinguishingSummary(a, common)
	require.NotContains(t, got, "severity=", "severity is excluded")
	require.Contains(t, got, "instance=web-1")
	require.Contains(t, got, "pod=p1")
	require.True(t, strings.HasPrefix(got, "instance="), "instance pinned first")
}

func TestView_NonPositiveDimensionsRenderNothing(t *testing.T) {
	t.Parallel()
	p := newPage(t, instance("fp-1", "warning", backend.AlertStateActive, nil))
	require.Empty(t, p.View(0, 20))
	require.Empty(t, p.View(80, 0))
}

func TestView_BodyHeightLeavesRoomForBandAndStrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		band  int
		strip int
	}{
		{name: "strip only", strip: 1},
		{name: "band and strip", band: 1, strip: 1},
		{name: "neither"},
		{name: "band only", band: 1},
	}
	const rows = 30
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bodyRows := 10 - 1 - tc.band - tc.strip
			in := make([]backend.Alert, 0, rows)
			for i := range rows {
				in = append(in, instance(fmt.Sprintf("fp-%02d", i), "warning", backend.AlertStateActive,
					map[string]string{sortKeyInstance: fmt.Sprintf("w%02d", i)}))
			}
			p := newPage(t, in...)
			if tc.band == 1 {
				p.BackendHealth = map[string]listpage.BackendHealth{tenant: {Detail: "boom"}}
			}
			if tc.strip == 0 {
				_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
			}
			lines := strings.Split(testutil.StripStyle(p.View(80, 10)), "\n")
			require.Len(t, lines, 10)
			require.Contains(t, lines[9], fmt.Sprintf("w%02d", bodyRows-1))

			_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift})
			lines = strings.Split(testutil.StripStyle(p.View(80, 10)), "\n")
			require.Equal(t, rows-bodyRows, p.TopRow())
			require.Len(t, lines, 10)
			require.Contains(t, lines[9], fmt.Sprintf("w%02d", rows-1))
		})
	}
}

func TestView_EmptyStateStacksBandAboveTheMessage(t *testing.T) {
	t.Parallel()
	const empty = "no instances — alert resolved (Esc to go back)"
	tests := []struct {
		name string
		band bool
	}{
		{name: "no band"},
		{name: "band", band: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPage(t)
			if tc.band {
				p.BackendHealth = map[string]listpage.BackendHealth{tenant: {Detail: "boom"}}
			}
			lines := strings.Split(testutil.StripStyle(p.View(80, 6)), "\n")
			if !tc.band {
				require.Equal(t, empty, strings.TrimRight(lines[0], " "))
				return
			}
			require.Contains(t, lines[0], "boom")
			require.Equal(t, empty, strings.TrimRight(lines[1], " "))
		})
	}
}

func TestRenderRows_CursorPrefixAndLineBreaks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		maxRows   int
		wantLines []string
	}{
		{name: "negative height renders nothing", maxRows: -1},
		{name: "zero height renders nothing", maxRows: 0},
		{name: "every row on its own line", maxRows: 5, wantLines: []string{"  ", "▸ ", "  "}},
		{name: "window stops at maxRows", maxRows: 2, wantLines: []string{"  ", "▸ "}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPage(t,
				instance("fp-1", "warning", backend.AlertStateActive, nil),
				instance("fp-2", "warning", backend.AlertStateActive, nil),
				instance("fp-3", "warning", backend.AlertStateActive, nil),
			)
			p.SetIndex(1, len(p.view))
			out := testutil.StripStyle(p.renderRows(p.scroll.Layout(p.columns(), 80), 80, tc.maxRows))
			if tc.wantLines == nil {
				require.Empty(t, out)
				return
			}
			lines := strings.Split(out, "\n")
			require.Len(t, lines, len(tc.wantLines))
			for i, prefix := range tc.wantLines {
				require.True(t, strings.HasPrefix(lines[i], prefix), "line %d %q", i, lines[i])
			}
		})
	}
}

func TestRender_AgeCell(t *testing.T) {
	t.Parallel()
	started := instance("fp-1", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst1})
	started.StartsAt = fixedNow.Add(-5 * time.Minute)
	unstarted := instance("fp-2", "warning", backend.AlertStateActive, map[string]string{sortKeyInstance: webInst2})
	unstarted.StartsAt = time.Time{}
	out := testutil.StripStyle(newPage(t, started, unstarted).View(120, 20))
	require.True(t, strings.HasSuffix(strings.TrimRight(rowContaining(t, out, webInst1), " "), "5m ago"))
	require.True(t, strings.HasSuffix(strings.TrimRight(rowContaining(t, out, webInst2), " "), "—"))
}

func TestColumns_AgeWidthFollowsTimeFormat(t *testing.T) {
	t.Parallel()
	ageMin := func(format timerender.Format) int {
		p := New(Options{Styles: pagetest.Styles(t), Tenant: tenant, AlertName: alertName, TimeFormat: format, Session: testutil.Session()})
		cols := p.columns()
		age := cols[len(cols)-1]
		require.Equal(t, sortKeyAge, age.Key)
		return age.Min
	}
	require.Greater(t, ageMin(timerender.Absolute), ageMin(timerender.Relative))
}

func TestSeverityOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{name: "set", labels: map[string]string{"severity": "critical"}, want: "critical"},
		{name: "empty", labels: map[string]string{"severity": ""}, want: "—"},
		{name: "absent", labels: map[string]string{}, want: "—"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, severityOf(backend.Alert{Labels: tc.labels}))
		})
	}
}

func TestLowerComposite_EmptyValues(t *testing.T) {
	t.Parallel()
	require.Equal(t, "\x00", lowerComposite(backend.Alert{Labels: map[string]string{"a": "", "b": ""}}))
}

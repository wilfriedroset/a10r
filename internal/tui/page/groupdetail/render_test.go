// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// Shared literals for the truncation tests — hoisted so the
// repeated short value and the discriminating tails don't trip
// goconst across the table cases and the render assertions.
const (
	dbInst   = "db-1"
	tail0042 = "0042"
	tail0117 = "0117"
)

// TestEllipsizeMiddle is the direct unit guard for the middle-out
// clipper: across every width regime — degenerate, the tail-ellipsis
// fallback boundary, the smallest middle split, wide, and multibyte —
// the result must NEVER exceed w cells (an over-wide result would
// re-introduce the column fusion this helper exists to prevent), and
// the discriminating head+tail must survive once a middle split is
// possible.
func TestEllipsizeMiddle(t *testing.T) {
	t.Parallel()
	const long = "node-pool-eu-west-1a-0042" // 25 cells

	tests := []struct {
		name string
		s    string
		w    int
		want string // "" means assert invariants only
	}{
		{name: "w<=0 returns empty", s: long, w: 0, want: ""},
		{name: "negative w returns empty", s: long, w: -3, want: ""},
		{name: "already fits returns unchanged", s: dbInst, w: 10, want: dbInst},
		{name: "exact fit returns unchanged", s: dbInst, w: 4, want: dbInst},
		{name: "w==1 tail-ellipsis fallback", s: long, w: 1},
		{name: "w==3 (suffix-width) fallback", s: long, w: 3},
		{name: "w==4 smallest middle split", s: long, w: 4},
		{name: "typical narrow middle split", s: long, w: 10},
		{name: "multibyte stays within width", s: "サービス-本番-0042", w: 8}, //nolint:gosmopolitan // deliberate wide-rune (CJK, width 2) case exercising width-aware truncation
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ellipsizeMiddle(tt.s, tt.w)
			if tt.w <= 0 {
				require.Empty(t, got)
				return
			}
			require.LessOrEqualf(t, lipgloss.Width(got), tt.w,
				"result %q exceeds width budget %d", got, tt.w)
			if tt.want != "" {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

// TestEllipsizeMiddle_PreservesDiscriminatingTail proves the whole
// point of the helper: two values sharing a long prefix but differing
// in the tail clip to DIFFERENT strings (a plain tail-ellipsis would
// collapse both to the shared head).
func TestEllipsizeMiddle_PreservesDiscriminatingTail(t *testing.T) {
	t.Parallel()
	a := ellipsizeMiddle("node-pool-eu-west-1a-0042", 12)
	b := ellipsizeMiddle("node-pool-eu-west-1b-0117", 12)
	require.NotEqual(t, a, b, "siblings sharing a prefix must clip distinguishably")
	require.Contains(t, a, tail0042)
	require.Contains(t, b, tail0117)
}

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

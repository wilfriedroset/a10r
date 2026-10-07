// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// highlightPage builds a page whose every row survives the filter
// "cpu", so the filtered render and the unfiltered render lay out the
// same columns and the two can be compared character by character.
func highlightPage(t *testing.T) *Page {
	t.Helper()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPUFirst", "critical", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("HighCPUSecond", "warning", backend.AlertStateActive, "f2", 2*time.Minute, nil),
	}})
	return p
}

// rawRowContaining returns the first rendered line whose visible text
// contains sub. rowContaining cannot be used here: the styling sits
// between the characters it searches for.
func rawRowContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for l := range strings.SplitSeq(out, "\n") {
		if strings.Contains(testutil.StripStyle(l), sub) {
			return l
		}
	}
	t.Fatalf("no rendered line shows %q\n%s", sub, testutil.StripStyle(out))
	return ""
}

// TestHighlight_KeepsColumnsPut is the layout contract: painting the
// match adds styling and nothing else, so the text and the column
// positions must come out byte for byte the same as the unfiltered
// render of the same rows.
func TestHighlight_KeepsColumnsPut(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	// 46 cells is narrow enough that ALERTNAME ellipsizes, so the run
	// also covers a match the cut falls inside.
	for _, width := range []int{100, 46} {
		require.True(t, p.SetFilter(""))
		p.recompute()
		plain := testutil.StripStyle(p.View(width, 24))
		for _, filter := range []string{"cpu", "~hcp", `\cpu`, "high.*u", "high"} {
			require.True(t, p.SetFilter(filter))
			p.recompute()
			require.Equal(t, plain, testutil.StripStyle(p.View(width, 24)),
				"filter %q moved the text at width %d", filter, width)
		}
	}
}

// TestHighlight_PaintsThePlainRow asserts a row with no row-level
// style takes the match colour on the characters that matched, and
// keeps the severity colour on the rest of its cell.
func TestHighlight_PaintsThePlainRow(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	require.True(t, p.SetFilter("high"))
	p.recompute()
	p.SetIndex(0, 2) // the cursor sits on the other row

	row := rawRowContaining(t, p.View(100, 24), "HighCPUSecond")
	require.Contains(t, row, p.styles.Table.MatchFg.Render("High"),
		"the matched characters must carry the match colour")
	require.NotContains(t, row, format.Emphasis("High"),
		"a plain row takes the colour, not the row-level emphasis")
}

// TestHighlight_PaintsInsideTheCursorRow asserts the cursor row keeps
// its own style across the match: the underline switches off with its
// own code, so the row colour is opened once and closed once and the
// row's bold runs to the end of the line.
func TestHighlight_PaintsInsideTheCursorRow(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	require.True(t, p.SetFilter("cpufirst"))
	p.recompute()
	p.SetIndex(0, 1)

	row := rawRowContaining(t, p.View(100, 24), "HighCPUFirst")
	require.Contains(t, row, format.Emphasis("CPUFirst"))
	require.NotContains(t, row, p.styles.Table.MatchFg.Render("CPUFirst"),
		"a colour change would fight the cursor background")
	require.Equal(t, 1, strings.Count(row, "\x1b[m")+strings.Count(row, "\x1b[0m"),
		"the row style must survive the match")
	require.NotContains(t, row, "\x1b[22", "the cursor row is bold and must stay bold")
	require.Equal(t, 100, lipgloss.Width(row))
}

// TestHighlight_PaintsInsideAMarkedRow covers the second row-level
// wrap. A marked row carries the skin's marked foreground, so the
// match takes the underline rather than a colour of its own.
func TestHighlight_PaintsInsideAMarkedRow(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	require.True(t, p.SetFilter("cpufirst"))
	p.recompute()
	p.SetIndex(0, 1)
	listpage.MarkOrCommit(&p.Base, p.groups, p.marks, markKey)
	p.SetIndex(1, 1) // move the cursor off the marked row

	row := rawRowContaining(t, p.View(100, 24), "HighCPUFirst")
	require.Contains(t, row, format.Emphasis("CPUFirst"))
	require.NotContains(t, row, p.styles.Table.MatchFg.Render("CPUFirst"))
	require.Equal(t, 100, lipgloss.Width(row))
}

// TestHighlight_FuzzyPaintsEachRun pins the fuzzy mode end to end:
// every matched rune is painted where it sits, scattered runs and all.
func TestHighlight_FuzzyPaintsEachRun(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	require.True(t, p.SetFilter("~hcpus"))
	p.recompute()
	p.SetIndex(0, 2) // the cursor sits on the other row

	row := rawRowContaining(t, p.View(100, 24), "HighCPUSecond")
	match := p.styles.Table.MatchFg
	require.Contains(t, row, match.Render("H"), "the first run is one rune")
	require.Contains(t, row, match.Render("CPUS"), "adjacent runes merge into one run")
}

// TestHighlight_PaintsOnlyWhatTheFilterMatchedOn is the
// one-classification contract: the rows the recompute kept and the
// characters the renderer painted are two reads of one compiled
// value, so the two halves cannot disagree about which grammar owns
// the buffer. A selector matches on label names the table does not
// render, so the rows narrow and the whole frame comes back unpainted.
func TestHighlight_PaintsOnlyWhatTheFilterMatchedOn(t *testing.T) {
	t.Parallel()

	p := highlightPage(t)
	matchSGR, _, _ := strings.Cut(p.styles.Table.MatchFg.Render("x"), "x")
	require.NotEmpty(t, matchSGR, "the match colour must carry an escape to look for")

	require.True(t, p.SetFilter("severity=warning"))
	p.recompute()
	out := p.View(100, 24)
	require.Contains(t, testutil.StripStyle(out), "HighCPUSecond")
	require.NotContains(t, testutil.StripStyle(out), "HighCPUFirst",
		"the selector is the predicate the recompute ran")
	require.NotContains(t, out, format.Emphasis("warning"))
	require.NotContains(t, out, matchSGR,
		"no rendered cell can be attributed to a label selector")

	require.True(t, p.SetFilter("cpu"))
	p.recompute()
	require.Contains(t, p.View(100, 24), matchSGR,
		"a text term owns its characters, so the cells it matched are painted")
}

// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// spansOf is a stand-in for filter.Matcher.MatchSpans: it reports
// every occurrence of needle. format must not import filter, so the
// helper takes the reporter as a function.
func spansOf(needle string) func(string) [][2]int {
	return func(s string) [][2]int {
		var out [][2]int
		for i := 0; ; {
			j := strings.Index(s[i:], needle)
			if j < 0 {
				return out
			}
			out = append(out, [2]int{i + j, i + j + len(needle)})
			i += j + len(needle)
		}
	}
}

func mark(s string) string { return "<" + s + ">" }

// TestHighlighter_Cell pins where the marks land. The visible text
// must survive every case unchanged, because a cell is already padded
// to its column width when the highlight runs.
func TestHighlighter_Cell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		hl   format.Highlighter
		text string
		want string
	}{
		{
			name: "no reporter leaves the text alone",
			hl:   format.Highlighter{Match: mark},
			text: "high cpu",
			want: "high cpu",
		},
		{
			name: "no mark function leaves the text alone",
			hl:   format.Highlighter{Spans: spansOf("cpu")},
			text: "high cpu",
			want: "high cpu",
		},
		{
			name: "a miss leaves the text alone",
			hl:   format.Highlighter{Spans: spansOf("disk"), Match: mark},
			text: "high cpu",
			want: "high cpu",
		},
		{
			name: "one hit in the middle",
			hl:   format.Highlighter{Spans: spansOf("cpu"), Match: mark},
			text: "high cpu ",
			want: "high <cpu> ",
		},
		{
			name: "hit at the head",
			hl:   format.Highlighter{Spans: spansOf("high"), Match: mark},
			text: "high cpu",
			want: "<high> cpu",
		},
		{
			name: "hit at the tail",
			hl:   format.Highlighter{Spans: spansOf("cpu"), Match: mark},
			text: "high cpu",
			want: "high <cpu>",
		},
		{
			name: "every hit is marked",
			hl:   format.Highlighter{Spans: spansOf("cpu"), Match: mark},
			text: "cpu cpu",
			want: "<cpu> <cpu>",
		},
		{
			name: "the search is case folded",
			hl:   format.Highlighter{Spans: spansOf("cpu"), Match: mark},
			text: "HIGH CPU",
			want: "HIGH <CPU>",
		},
		{
			name: "a span past the end is clipped",
			hl:   format.Highlighter{Spans: func(string) [][2]int { return [][2]int{{4, 99}} }, Match: mark},
			text: "high cpu",
			want: "high< cpu>",
		},
		{
			name: "a span that starts past the end is dropped",
			hl:   format.Highlighter{Spans: func(string) [][2]int { return [][2]int{{99, 120}} }, Match: mark},
			text: "high cpu",
			want: "high cpu",
		},
		{
			name: "overlapping spans do not double mark",
			hl:   format.Highlighter{Spans: func(string) [][2]int { return [][2]int{{0, 4}, {2, 6}} }, Match: mark},
			text: "high cpu",
			want: "<high> cpu",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.hl.Cell(tc.text, lipgloss.Style{}))
		})
	}
}

// TestHighlighter_CellKeepsBaseStyle asserts the parts outside the
// span keep the cell's own colour and the marked part takes the
// match style. The two styles sit side by side, never nested, so no
// reset from the inner style can end the outer one.
func TestHighlighter_CellKeepsBaseStyle(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	match := lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	hl := format.Highlighter{Spans: spansOf("cpu"), Match: format.MatchStyle(match)}

	got := hl.Cell("high cpu load", base)
	require.Equal(t, "high cpu load", testutil.StripStyle(got))
	require.Equal(t, 13, lipgloss.Width(got))
	require.Contains(t, got, match.Render("cpu"))
	require.Contains(t, got, base.Render("high "))
	require.Contains(t, got, base.Render(" load"))
}

// TestHighlighter_CellSkipsPreStyledText guards the offset contract:
// a cell its producer already coloured carries escape bytes that the
// byte ranges know nothing about, so the highlight stands down rather
// than paint inside an escape sequence.
func TestHighlighter_CellSkipsPreStyledText(t *testing.T) {
	t.Parallel()

	pre := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("high cpu")
	hl := format.Highlighter{Spans: spansOf("cpu"), Match: mark}
	require.Equal(t, pre, hl.Cell(pre, lipgloss.Style{}))
}

// TestHighlighter_CellSkipsFoldThatMovesBytes covers the one case
// where a lower-cased copy changes length: every offset past the fold
// would shift, so the cell renders unmarked instead of wrong.
func TestHighlighter_CellSkipsFoldThatMovesBytes(t *testing.T) {
	t.Parallel()

	const text = "İstanbul cpu" // U+0130 folds to a one-byte i
	require.NotEqual(t, len(text), len(strings.ToLower(text)))
	hl := format.Highlighter{Spans: spansOf("cpu"), Match: mark}
	require.Equal(t, text, hl.Cell(text, lipgloss.Style{}))
}

// TestEmphasis keeps a matched run readable inside a row the page
// wrapped in one style: the underline switches off with its own code,
// so the row colour survives the run instead of ending at the first
// reset.
func TestEmphasis(t *testing.T) {
	t.Parallel()

	row := lipgloss.NewStyle().
		Foreground(lipgloss.Color("9")).
		Background(lipgloss.Color("4")).
		Render("high " + format.Emphasis("cpu") + " load")

	require.Equal(t, "high cpu load", testutil.StripStyle(row))
	require.NotContains(t, format.Emphasis("cpu"), "\x1b[0m")
	require.NotContains(t, format.Emphasis("cpu"), "\x1b[m")
	// The row style is opened once and closed once: nothing inside
	// the run ends it early.
	require.Equal(t, 1, strings.Count(row, "\x1b[m")+strings.Count(row, "\x1b[0m"))
}

// TestEmphasisKeepsTheRowBold guards against SGR 22. The cursor row
// is bold by skin (k9s parity), and 22 means normal intensity, not
// "undo the bold I just set" — closing the run with it would switch
// the row's own bold off for every character after the match.
func TestEmphasisKeepsTheRowBold(t *testing.T) {
	t.Parallel()

	row := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("9")).
		Render("high " + format.Emphasis("cpu") + " load")

	require.Equal(t, "high cpu load", testutil.StripStyle(row))
	require.NotContains(t, row, "\x1b[22", "SGR 22 drops the bold the row set itself")
}

// TestHighlighter_CellSkipsFoldThatCancelsOut covers the fold check
// the total length misses: one rune that grows and one that shrinks
// leave the byte count untouched while every offset between them
// moves. Painting on those offsets would cut a rune in half and the
// cell would stop being valid UTF-8.
func TestHighlighter_CellSkipsFoldThatCancelsOut(t *testing.T) {
	t.Parallel()

	const text = "\u023a\u0130cpu"
	require.Len(t, strings.ToLower(text), len(text))
	hl := format.Highlighter{Spans: spansOf("cpu"), Match: mark}
	require.Equal(t, text, hl.Cell(text, lipgloss.Style{}))
}

// TestHighlighter_CellStopsAtTheContentEnd holds the column gap. A
// cell arrives padded to its width, so a span that runs to the end of
// the string — what a trailing `.*` reports — must stop at the last
// character, or a cursor row shows an underline crossing the gap.
func TestHighlighter_CellStopsAtTheContentEnd(t *testing.T) {
	t.Parallel()

	all := func(s string) [][2]int { return [][2]int{{0, len(s)}} }
	hl := format.Highlighter{Spans: all, Match: mark}
	pad := strings.Repeat(" ", 6)
	require.Equal(t, "<cpu>"+pad, hl.Cell("cpu"+pad, lipgloss.Style{}))
}

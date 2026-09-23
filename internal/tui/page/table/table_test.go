// SPDX-License-Identifier: Apache-2.0

package table_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/table"
)

// threeCols is three fixed 5-cell columns, so the arithmetic stays
// readable: n columns need n*5 cells plus n-1 separators, on top of
// the 4-cell row prefix every frame reserves.
func threeCols() []table.Column {
	return []table.Column{
		{Key: "a", Title: "A", Min: 5, Content: 5},
		{Key: "b", Title: "B", Min: 5, Content: 5},
		{Key: "c", Title: "C", Min: 5, Content: 5},
	}
}

// A title cut down to "CLU" with no room for the arrow lies about
// both the column and the live sort direction, so the header is the
// floor the allocator starts from (ADR 0048).
func TestLayout_AColumnIsNeverNarrowerThanItsHeader(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	sortable := s.Layout([]table.Column{{Key: "a", Title: "CLUSTER", Sortable: true, Min: 3, Content: 3}}, 100)
	require.Equal(t, 9, sortable.WidthOf("a"))

	plain := s.Layout([]table.Column{{Key: "a", Title: "CLUSTER", Min: 3, Content: 3}}, 100)
	require.Equal(t, 7, plain.WidthOf("a"))
}

// WidthOf answers 0 for a column the horizontal window dropped, so a
// page that clips a cell itself can tell "no room" from "not here".
func TestLayout_WidthOfIsZeroForAColumnOutOfView(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	l := s.Layout(threeCols(), 100)
	require.Equal(t, 5, l.WidthOf("c"))
	require.Zero(t, l.WidthOf("nosuch"))

	// 15 leaves an 11-cell budget, which two columns fit — until the
	// clipped row takes a cell back for the ">" marker.
	narrow := s.Layout(threeCols(), 15)
	require.Equal(t, 5, narrow.WidthOf("a"))
	require.Zero(t, narrow.WidthOf("b"))
	require.Zero(t, narrow.WidthOf("c"))
}

func TestLayout_HeaderCarriesTheArrowOfTheActiveColumn(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	l := s.Layout(threeCols(), 100)
	got := l.Header(table.Sort{
		Arrow:  func(key string) string { return map[string]string{"b": "↑"}[key] },
		Active: func(key string) bool { return key == "b" },
	}, table.Chrome{})
	require.Contains(t, got, "B ↑")
	require.NotContains(t, got, "A ↑")
}

// The zero Sort renders every column plain, so a page with no sorter
// can still paint a header.
func TestLayout_HeaderWithoutASorter(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	require.Contains(t, s.Layout(threeCols(), 100).Header(table.Sort{}, table.Chrome{}), "A")
}

// A row shorter than the column set renders its tail empty rather
// than stopping early, so no page needs a length guard.
func TestLayout_RowRendersAShortCellSliceToTheEnd(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	l := s.Layout(threeCols(), 100)
	got := l.Row(table.Row{Prefix: "  ✓ ", Cells: []table.Cell{{Text: "one"}}}, 30)
	require.Equal(t, "  ✓ one", strings.TrimRight(got, " "))
	require.Equal(t, 30, lipgloss.Width(got))
}

// Paint runs on the clipped text and before the pad, so a trailing
// match never paints the gap between two columns.
func TestLayout_PaintSeesTheClippedTextOnly(t *testing.T) {
	t.Parallel()

	cols := []table.Column{{Key: "a", Title: "A", Min: 4, Content: 4, Clip: table.ClipEllipsis}}
	var s table.Scroll
	l := s.Layout(cols, 12)
	seen := ""
	got := l.Row(table.Row{Cells: []table.Cell{{
		Text:  "abcdefgh",
		Paint: func(shown string) string { seen = shown; return format.Emphasis(shown) },
	}}}, 12)
	require.Equal(t, "abc…", seen)
	require.Contains(t, got, format.Emphasis("abc…"))
	require.Equal(t, 12, lipgloss.Width(got), "Paint must not change the rendered width")
}

// A plain column cuts an over-wide cell too, and the cut belongs
// before the paint. SGRTruncate closes a style it cuts through with a
// reset, and a reset in the middle of the line ends the row-level
// style for every column after it.
func TestLayout_APlainCellIsCutBeforeItIsPainted(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	l := s.Layout([]table.Column{
		{Key: "a", Title: "A", Min: 4, Content: 4},
		{Key: "b", Title: "B", Min: 4, Content: 4},
	}, 13)
	seen := ""
	got := l.Row(table.Row{Cells: []table.Cell{
		{Text: "abcdefgh", Paint: func(shown string) string { seen = shown; return format.Emphasis(shown) }},
		{Text: "xy"},
	}}, 13)
	require.Equal(t, "abcd", seen)
	require.NotContains(t, got, "\x1b[0m", "a cut style must not reset mid-line")
	require.Contains(t, got, "xy")
	require.Equal(t, 13, lipgloss.Width(got), "Paint must not change the rendered width")
}

// ClipPad cuts at the column edge without spending a content cell on
// saying so; ClipEllipsis marks the cut.
func TestLayout_ClipModes(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	l := s.Layout([]table.Column{
		{Key: "a", Title: "A", Min: 4, Content: 4},
		{Key: "b", Title: "B", Min: 4, Content: 4, Clip: table.ClipEllipsis},
	}, 13)
	got := l.Row(table.Row{Cells: []table.Cell{{Text: "abcdefgh"}, {Text: "abcdefgh"}}}, 13)
	require.Equal(t, "abcd abc…", strings.TrimRight(got, " "))
}

// A key press banks a direction and the next frame resolves it, so
// the move is judged against the width the operator is looking at.
func TestScroll_StepIsResolvedByTheNextLayout(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	s.Step(1)
	require.Zero(t, s.Offset)
	l := s.Layout(threeCols(), 20)
	require.Equal(t, 1, s.Offset)
	require.Equal(t, 5, l.WidthOf("c"))
	require.Zero(t, l.WidthOf("b"))
}

// A row that already fits has nowhere to scroll, so the banked press
// is dropped rather than held against a later narrow frame.
func TestScroll_StepOnAFittingRowBanksNothing(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	for range 4 {
		s.Step(1)
	}
	_ = s.Layout(threeCols(), 100)
	require.Zero(t, s.Offset)
}

// Left walks back one column per press from wherever the window
// actually sits, so it is never dead after a run of right presses the
// window saturated on.
func TestScroll_StepBackRecoversTheLeftEdge(t *testing.T) {
	t.Parallel()

	var s table.Scroll
	for range 9 {
		s.Step(1)
	}
	_ = s.Layout(threeCols(), 20)
	require.Equal(t, 1, s.Offset)
	s.Step(-1)
	_ = s.Layout(threeCols(), 20)
	require.Zero(t, s.Offset)
}

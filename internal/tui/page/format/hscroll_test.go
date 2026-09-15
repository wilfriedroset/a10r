// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// fiveCols is five columns with a 10-cell floor, so the arithmetic
// stays readable: n columns need n*10 cells plus n-1 separators.
func fiveCols() []format.Column {
	out := make([]format.Column, 0, 5)
	for range 5 {
		out = append(out, format.Column{Min: 10, Content: 10})
	}
	return out
}

func TestWindowAt_EverythingFitsShowsEveryColumn(t *testing.T) {
	t.Parallel()

	w := format.WindowAt(fiveCols(), 54, 1, 0)
	require.Equal(t, []int{0, 1, 2, 3, 4}, w.Cols)
	require.False(t, w.ClipLeft)
	require.False(t, w.ClipRight)
}

// An offset is ignored while the row fits, so the arrow keys cannot
// hide a column the operator can already see.
func TestWindowAt_OffsetIsIgnoredWhileEverythingFits(t *testing.T) {
	t.Parallel()

	require.Equal(t, []int{0, 1, 2, 3, 4}, format.WindowAt(fiveCols(), 54, 1, 3).Cols)
}

// The first column is pinned: it stays in view at every offset,
// because it carries the row's identity.
func TestWindowAt_PinsTheFirstColumn(t *testing.T) {
	t.Parallel()

	w := format.WindowAt(fiveCols(), 32, 1, 2)
	require.Equal(t, 0, w.Cols[0])
	require.Equal(t, []int{0, 3, 4}, w.Cols)
	require.True(t, w.ClipLeft)
	require.False(t, w.ClipRight)
}

func TestWindowAt_ClipsTheRightEdge(t *testing.T) {
	t.Parallel()

	w := format.WindowAt(fiveCols(), 32, 1, 0)
	require.Equal(t, []int{0, 1, 2}, w.Cols)
	require.False(t, w.ClipLeft)
	require.True(t, w.ClipRight)
}

// An offset past the last column still paints one scrollable column,
// so the operator never lands on a row that is only the pinned cell.
func TestWindowAt_OffsetStopsAtTheLastColumn(t *testing.T) {
	t.Parallel()

	w := format.WindowAt(fiveCols(), 32, 1, 99)
	require.Equal(t, []int{0, 4}, w.Cols)
	require.True(t, w.ClipLeft)
	require.False(t, w.ClipRight)
}

func TestWindowAt_NoColumns(t *testing.T) {
	t.Parallel()

	require.Empty(t, format.WindowAt(nil, 40, 1, 0).Cols)
}

// A budget too small for even the pinned column still returns it:
// Distribute owns the shrink from there, so the window never hands
// the renderer an empty row.
func TestWindowAt_TinyBudgetKeepsThePinnedColumn(t *testing.T) {
	t.Parallel()

	w := format.WindowAt(fiveCols(), 4, 1, 0)
	require.Equal(t, []int{0}, w.Cols)
	require.True(t, w.ClipRight)
}

// A single column is the pinned column, so there is nothing to
// scroll and nothing to mark as clipped.
func TestWindowAt_SingleColumnHasNothingToScroll(t *testing.T) {
	t.Parallel()

	w := format.WindowAt([]format.Column{{Min: 40, Content: 40}}, 4, 1, 7)
	require.Equal(t, []int{0}, w.Cols)
	require.False(t, w.ClipLeft)
	require.False(t, w.ClipRight)
}

// A flex column claims only its floor, so a long value in one does
// not push the row into a scroll it does not need.
func TestWindowAt_FlexContentDoesNotTriggerScroll(t *testing.T) {
	t.Parallel()

	cols := []format.Column{
		{Min: 10, Content: 10},
		{Min: 10, Content: 400, Weight: 1},
		{Min: 10, Content: 10},
	}
	require.Equal(t, []int{0, 1, 2}, format.WindowAt(cols, 32, 1, 1).Cols)
}

// A fixed column claims max(Min, Content), which is what Distribute
// reserves for it. Claiming only Min here would leave a band of
// widths where the window says "everything fits" and Distribute still
// shrinks every column below its floor.
func TestWindowAt_FixedContentCountsTowardTheReservation(t *testing.T) {
	t.Parallel()

	cols := []format.Column{
		{Min: 10, Content: 10},
		{Min: 10, Content: 30},
		{Min: 10, Content: 10},
	}
	w := format.WindowAt(cols, 32, 1, 0)
	require.Equal(t, []int{0}, w.Cols)
	require.True(t, w.ClipRight)
}

// Total lets a caller that knows a column by its position in the
// full slice find it again inside the window.
func TestWindowAt_ReportsTheFullColumnCount(t *testing.T) {
	t.Parallel()

	cols := fiveCols()
	require.Equal(t, len(cols), format.WindowAt(cols, 200, 1, 0).Total)
	require.Equal(t, len(cols), format.WindowAt(cols, 30, 1, 0).Total)
	require.Equal(t, 1, format.WindowAt(cols[:1], 2, 1, 0).Total)
}

// Right needs a clipped row, so the key is dead on a terminal that
// shows every column.
func TestScroll_RightNeedsAClippedRow(t *testing.T) {
	t.Parallel()

	s := format.Scroll{}
	s.Right(format.Window{Total: 6})
	require.Zero(t, s.Offset)
}

// The zero Window is reachable from any caller now that Scroll is
// exported, and a page with no columns must not move.
func TestScroll_ZeroWindowIsInert(t *testing.T) {
	t.Parallel()

	s := format.Scroll{}
	s.Right(format.Window{})
	s.Left(format.Window{})
	require.Zero(t, s.Offset)
}

func TestScroll_RightMovesOn(t *testing.T) {
	t.Parallel()

	s := format.Scroll{}
	s.Right(format.Window{Total: 6, ClipRight: true})
	require.Equal(t, 1, s.Offset)
}

// Right stops once the last column is in view, so the row never
// scrolls into a window that holds only the pinned column.
func TestScroll_RightStopsAtTheLastColumn(t *testing.T) {
	t.Parallel()

	s := format.Scroll{Offset: 9}
	s.Right(format.Window{Total: 6, ClipRight: true})
	require.Equal(t, 5, s.Offset)
}

func TestScroll_LeftStopsAtZero(t *testing.T) {
	t.Parallel()

	s := format.Scroll{}
	s.Left(format.Window{Total: 6})
	require.Zero(t, s.Offset)
}

// An offset left over from a wider column set has to come back within
// one press, not after as many presses as the operator made.
func TestScroll_LeftRecoversFromAStaleOffset(t *testing.T) {
	t.Parallel()

	s := format.Scroll{Offset: 9}
	s.Left(format.Window{Total: 6})
	require.Equal(t, 3, s.Offset)
}

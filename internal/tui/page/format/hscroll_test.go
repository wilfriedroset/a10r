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

// A row that shows every column has nowhere to move to.
func TestWindowAt_CanRightIsFalseWhenEverythingFits(t *testing.T) {
	t.Parallel()

	require.False(t, format.WindowAt(fiveCols(), 54, 1, 0).CanRight)
}

func TestWindowAt_CanRightOnAClippedRow(t *testing.T) {
	t.Parallel()

	require.True(t, format.WindowAt(fiveCols(), 32, 1, 0).CanRight)
}

// A budget too small for a second column drops one at every offset,
// so the marker belongs, but no offset ever paints more than the
// pinned column.
func TestWindowAt_CanRightIsFalseWithOnlyThePinnedColumn(t *testing.T) {
	t.Parallel()

	w := format.WindowAt(fiveCols(), 4, 1, 0)
	require.True(t, w.ClipRight)
	require.False(t, w.CanRight)
}

// A single column wider than the whole budget empties the window at
// the offset it starts at, because WindowAt stops at the first column
// that does not fit. The columns behind it are still reachable, so
// the row must keep scrolling rather than parking on the fat one.
func TestWindowAt_CanRightStepsOverAnUnfittableColumn(t *testing.T) {
	t.Parallel()

	cols := []format.Column{
		{Min: 10, Content: 10},
		{Min: 30, Content: 30},
		{Min: 10, Content: 10},
	}
	w := format.WindowAt(cols, 25, 1, 0)
	require.Equal(t, []int{0}, w.Cols)
	require.True(t, w.ClipRight)
	require.True(t, w.CanRight)
	require.Equal(t, []int{0, 2}, format.WindowAt(cols, 25, 1, 1).Cols)

	// Two fat columns in a row: the press off offset 0 paints the same
	// window again. Reaching the last column is still worth the press,
	// so CanRight leads the operator through rather than parking.
	fatter := []format.Column{
		{Min: 10, Content: 10},
		{Min: 30, Content: 30},
		{Min: 30, Content: 30},
		{Min: 5, Content: 5},
	}
	require.True(t, format.WindowAt(fatter, 25, 1, 0).CanRight)
	require.Equal(t, []int{0}, format.WindowAt(fatter, 25, 1, 1).Cols)
	require.Equal(t, []int{0, 3}, format.WindowAt(fatter, 25, 1, 2).Cols)
}

// A window holding only the pinned column says nothing about which
// columns the operator already scrolled past, so the scan starts at
// the offset rather than at the painted edge. Here the column behind
// the offset still fits, and answering with it would bank a press the
// window saturates on.
func TestWindowAt_CanRightIgnoresTheColumnsScrolledPast(t *testing.T) {
	t.Parallel()

	cols := []format.Column{
		{Min: 10, Content: 10},
		{Min: 5, Content: 5},
		{Min: 30, Content: 30},
	}
	w := format.WindowAt(cols, 25, 1, 1)
	require.Equal(t, []int{0}, w.Cols)
	require.True(t, w.ClipRight)
	require.False(t, w.CanRight)
}

// The columns already in view trivially fit beside the pinned one, so
// a reachability scan that starts before the right edge answers with
// a column the operator is already looking at. Here only the painted
// pair fits and the last column never can, so Right would drop the
// leftmost scrolled column and bring nothing back.
func TestWindowAt_CanRightIgnoresTheColumnsAlreadyInView(t *testing.T) {
	t.Parallel()

	cols := []format.Column{
		{Min: 10, Content: 10},
		{Min: 5, Content: 5},
		{Min: 5, Content: 5},
		{Min: 30, Content: 30},
	}
	w := format.WindowAt(cols, 25, 1, 0)
	require.Equal(t, []int{0, 1, 2}, w.Cols)
	require.True(t, w.ClipRight)
	require.False(t, w.CanRight)
}

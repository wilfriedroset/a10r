// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var (
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
	keyLeft  = tea.KeyPressMsg{Code: tea.KeyLeft}
)

// narrowWidth is under the sum of the column floors, which is what
// opens the horizontal scroll. wideWidth clears them all.
const (
	narrowWidth = 40
	wideWidth   = 160
)

func scrollPage(t *testing.T) *Page {
	t.Helper()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("DiskFull", "critical", backend.AlertStateActive, "fp1", 0, nil),
	}})
	return p
}

// narrowHeader renders the page narrow and returns its header line.
func narrowHeader(t *testing.T, p *Page) string {
	t.Helper()
	return rowContaining(t, testutil.StripStyle(p.View(narrowWidth, 20)), "SEVERITY")
}

// A row too wide for the terminal drops columns off the right rather
// than shrinking every column below its floor.
func TestHScroll_NarrowRowClipsTheRightEdge(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	header := narrowHeader(t, p)
	require.Contains(t, header, "ALERTNAME")
	require.NotContains(t, header, "AGE")
	require.Contains(t, header, ">")
}

// Right scrolls by one column with the first data column pinned, so
// the row keeps its identity at every offset.
func TestHScroll_RightPinsTheFirstColumn(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	// The scroll keys read the width of the last painted frame, so
	// the page has to render narrow before the arrow lands.
	_ = p.View(narrowWidth, 20)
	_, _ = p.Update(keyRight)
	header := narrowHeader(t, p)
	require.Contains(t, header, "SEVERITY")
	require.NotContains(t, header, "ALERTNAME")
	require.Contains(t, header, "<")
}

func TestHScroll_LeftScrollsBack(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	before := p.View(narrowWidth, 20)
	_, _ = p.Update(keyRight)
	require.NotEqual(t, before, p.View(narrowWidth, 20))
	_, _ = p.Update(keyLeft)
	require.Equal(t, before, p.View(narrowWidth, 20))
}

// Left at the leftmost offset has nowhere to go.
func TestHScroll_LeftAtTheEdgeChangesNothing(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	before := p.View(narrowWidth, 20)
	_, _ = p.Update(keyLeft)
	require.Equal(t, before, p.View(narrowWidth, 20))
	// The offset itself, not only the frame: a negative offset paints
	// the same columns as zero, so the rendered row hides the bug.
	require.Zero(t, p.hscrollOffset)
}

// On a terminal wide enough for every column the arrows do nothing,
// so they never hide a column the operator can already see.
func TestHScroll_ArrowsAreNoOpsWhenEverythingFits(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	before := p.View(wideWidth, 20)
	_, _ = p.Update(keyRight)
	_, _ = p.Update(keyRight)
	require.Equal(t, before, p.View(wideWidth, 20))
}

// Scrolling must not take the sort arrow with it: h and l keep the
// sort walk, which is a different axis from the view window.
func TestHScroll_ArrowsDoNotWalkTheSortColumns(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	_ = p.View(narrowWidth, 20)
	before := p.sorter.ActiveKey()
	_, _ = p.Update(keyRight)
	_, _ = p.Update(keyLeft)
	require.Equal(t, before, p.sorter.ActiveKey())
}

// Right stops once the last column is in view, so the row never
// scrolls into a window that holds only the pinned column.
func TestHScroll_RightStopsAtTheLastColumn(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	for range 10 {
		_ = p.View(narrowWidth, 20)
		_, _ = p.Update(keyRight)
	}
	header := narrowHeader(t, p)
	require.Contains(t, header, "AGE")
	require.NotContains(t, header, ">")
}

// An arrow pressed while everything fits banks no offset, so a later
// resize down does not open on a far-right window the operator never
// scrolled to.
func TestHScroll_RightOnAFittingRowBanksNoOffset(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	_ = p.View(wideWidth, 20)
	for range 4 {
		_, _ = p.Update(keyRight)
	}
	require.Contains(t, narrowHeader(t, p), "ALERTNAME")
}

// The "<" and ">" markers say that a column is out of view, not which
// key brings it back, so the `?` overlay has to carry the arrows.
func TestHScroll_ArrowsAreAdvertised(t *testing.T) {
	t.Parallel()

	for _, b := range newPage(t).Bindings() {
		if b.ChipKey() == "←/→" {
			// The dispatcher matches Key against the incoming event,
			// so the pair label belongs in DisplayKey and never there.
			require.Equal(t, "Right", b.Key)
			return
		}
	}
	t.Fatal("no binding advertises the column scroll")
}

// A column set that shrinks under a scrolled offset leaves the offset
// past the last window. Without a clamp on the way back, Left is dead
// for as many presses as the operator made.
func TestHScroll_LeftRecoversAfterTheColumnSetShrinks(t *testing.T) {
	t.Parallel()

	p := widePage(t, "")
	_, _ = p.Update(keyWide)
	for range 10 {
		_ = p.View(narrowWidth, 20)
		_, _ = p.Update(keyRight)
	}
	_, _ = p.Update(keyWide)
	atEnd := narrowHeader(t, p)
	_, _ = p.Update(keyLeft)
	require.NotEqual(t, atEnd, narrowHeader(t, p), "one Left must move the window back")
}

// On a terminal too narrow for two columns every offset reports a
// clipped right edge, so the counter needs a bound of its own.
// Without one it runs away and Left is dead for as many presses as
// the operator made.
func TestHScroll_OffsetStopsGrowingOnATinyTerminal(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	for range 10 {
		_ = p.View(20, 20)
		_, _ = p.Update(keyRight)
	}
	atEnd := narrowHeader(t, p)
	_, _ = p.Update(keyLeft)
	require.NotEqual(t, atEnd, narrowHeader(t, p), "one Left must move the window back")
}

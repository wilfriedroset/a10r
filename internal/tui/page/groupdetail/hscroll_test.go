// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
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
	return newColumnPage(t, []config.Column{{Label: podKey}},
		podInstance("fp-1", "web-a"))
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

	header := narrowHeader(t, scrollPage(t))
	require.Contains(t, header, "INSTANCE")
	require.NotContains(t, header, "AGE")
	require.Contains(t, header, ">")
}

// Right scrolls by one column with SEVERITY pinned, so the row keeps
// its identity at every offset. No TENANT column on this page, so the
// pinned column never shifts.
func TestHScroll_RightPinsTheFirstColumn(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	// The scroll keys read the width of the last painted frame, so
	// the page has to render narrow before the arrow lands.
	_ = p.View(narrowWidth, 20)
	_, _ = p.Update(keyRight)
	header := narrowHeader(t, p)
	require.Contains(t, header, "SEVERITY")
	require.NotContains(t, header, "INSTANCE")
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
	// One assertion per arrow, not one for a there-and-back pair: the
	// sort walk wraps, so Right then Left lands on the starting
	// column whether or not the page consumes the keys.
	_, _ = p.Update(keyRight)
	require.Equal(t, before, p.sorter.ActiveKey())
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
	require.Contains(t, narrowHeader(t, p), "INSTANCE")
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

// The row cursor and the mark tick live in the same four cells the
// "<" marker rides on the header, so a scrolled row must keep both.
// The cells themselves must follow the window: a row that paints its
// leftmost cells under a scrolled header lies about every column.
func TestHScroll_ScrolledRowFollowsTheWindow(t *testing.T) {
	t.Parallel()

	p := scrollPage(t)
	_ = p.View(narrowWidth, 20)
	_, _ = p.Update(keyRight)
	row := rowContaining(t, testutil.StripStyle(p.View(narrowWidth, 20)), "▸")
	require.Contains(t, row, "warning", "the pinned SEVERITY cell stays")
	require.Contains(t, row, "web-a", "the scrolled-to POD cell paints")
	require.Contains(t, row, "active", "so does the STATE cell behind it")
}

// SPDX-License-Identifier: Apache-2.0

package table

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// Scroll is a table's horizontal position plus the key intent that
// has not met a frame yet, both measured against the column set of
// the last layout.
type Scroll struct {
	// Offset is how many columns the row is scrolled past the pinned
	// first one.
	Offset int
	// pending is the net column intent banked since the last layout.
	// No width reaches a page at key time, so a press cannot tell
	// whether the move is legal and Layout decides instead.
	pending int
	lastCols int
}

// Step banks one column of intent: -1 for left, +1 for right.
func (s *Scroll) Step(d int) { s.pending += d }

// resolve drops an offset that was measured against a different
// column set, then applies the banked intent one column at a time,
// re-reading the window at each step, so a step with nowhere to land
// is dropped rather than banked and clamped later.
func (s *Scroll) resolve(sp []format.Column, budget int) {
	// Answering a column set the operator changed with the far-right
	// edge of the old one hides what the change was about.
	if s.lastCols != len(sp) {
		s.lastCols = len(sp)
		s.Offset = 0
	}
	for ; s.pending > 0; s.pending-- {
		win, _ := window(sp, budget, s.Offset)
		if win.CanRight {
			s.Offset = clamped(s.Offset, win) + 1
		}
	}
	for ; s.pending < 0; s.pending++ {
		win, _ := window(sp, budget, s.Offset)
		s.Offset = max(0, clamped(s.Offset, win)-1)
	}
}

// clamped pulls a stale offset back to the last value that still
// moves the window. format.WindowAt saturates past that, so an
// unclamped counter leaves a left press dead for as many presses as
// the operator made going right.
func clamped(offset int, win format.Window) int {
	return min(offset, max(0, win.Total-2))
}

// Horizontal-scroll markers. The header carries them because it is
// the one line that is not row data: dropping a column the operator
// configured without saying so is worse than spending a cell on the
// marker (ADR 0048).
const (
	scrollLeftMarker  = "<"
	scrollRightMarker = ">"
)

// scrollPrefix is the header's row prefix. It carries the left marker
// in the cells the data rows spend on the cursor arrow and the mark
// glyph, so the marker costs no column width.
func scrollPrefix(clipLeft bool) string {
	if !clipLeft {
		return strings.Repeat(" ", format.RowPrefixCols)
	}
	return format.PadRight("  "+scrollLeftMarker, format.RowPrefixCols)
}

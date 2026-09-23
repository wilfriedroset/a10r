// SPDX-License-Identifier: Apache-2.0

package table

import "github.com/wilfriedroset/a10r/internal/tui/page/format"

// Scroll is a table's horizontal position plus the key intent that
// has not met a frame yet.
type Scroll struct {
	// Offset is how many columns the row is scrolled past the pinned
	// first one.
	Offset int
	// pending is the net column intent banked since the last layout.
	// No width reaches a page at key time, so a press cannot tell
	// whether the move is legal and Layout decides instead.
	pending int
}

// Step banks one column of intent: -1 for left, +1 for right.
func (s *Scroll) Step(d int) { s.pending += d }

// resolve applies the banked intent one column at a time, re-reading
// the window at each step, so a step with nowhere to land is dropped
// rather than banked and clamped later.
func (s *Scroll) resolve(sp []format.Column, budget int) {
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

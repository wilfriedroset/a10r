// SPDX-License-Identifier: Apache-2.0

package format

// Window is the set of columns a horizontally scrolled row paints.
type Window struct {
	// Cols are indices into the caller's full Column slice, in render
	// order. The first entry is always column 0, which is pinned.
	Cols []int
	// Total is the column count the window was chosen from, so a
	// caller that knows a column by its position in the full slice
	// (the renderer locates STATE as "second to last") can still find
	// it once the window renumbers everything.
	Total int
	// ClipLeft and ClipRight report a column dropped off that edge, so
	// the header can mark it. Dropping a column the operator asked for
	// without saying so is worse than a marker (ADR 0048).
	ClipLeft, ClipRight bool
}

// WindowAt picks the columns that fit budget, with column 0 pinned
// and the next offset columns scrolled past. It returns every column
// when the row already fits, so the arrow keys are no-ops there.
//
// budget and separator carry the same meaning as in Distribute, and
// the caller feeds the chosen subset back to Distribute: this
// function only decides which columns are in view, never how wide
// they are.
func WindowAt(cols []Column, budget, separator, offset int) Window {
	if len(cols) == 0 {
		return Window{}
	}
	if reserved(cols, separator) <= budget {
		all := make([]int, len(cols))
		for i := range all {
			all[i] = i
		}
		return Window{Cols: all, Total: len(cols)}
	}

	if len(cols) == 1 {
		return Window{Cols: []int{0}, Total: 1}
	}

	sep := max(0, separator)
	// The pinned column is taken before the budget check, so a
	// terminal too narrow for even one column still renders the row
	// identity and leaves the shrink to Distribute.
	out := []int{0}
	used := colWidth(cols[0])

	start := min(max(1, 1+offset), len(cols)-1)
	last := 0
	for i := start; i < len(cols); i++ {
		next := used + sep + colWidth(cols[i])
		if next > budget {
			break
		}
		out = append(out, i)
		used, last = next, i
	}
	return Window{
		Cols:      out,
		Total:     len(cols),
		ClipLeft:  start > 1,
		ClipRight: last < len(cols)-1,
	}
}

// reserved is the sum of the column floors plus the separators. Under
// that sum Distribute falls back to its proportional shrink, which
// takes every column below its floor at once. Horizontal scroll
// replaces that: fewer columns, each still readable.
//
// The per-column claim mirrors Distribute step 1 exactly — max(Min,
// Content) for a fixed column, Min for a flex one — so the two agree
// on where the shrink starts. Claiming Min for every column instead
// would leave a band of widths where this reports "everything fits"
// and Distribute still shrinks below the floors.
func reserved(cols []Column, separator int) int {
	total := (len(cols) - 1) * max(0, separator)
	for _, c := range cols {
		total += colWidth(c)
	}
	return total
}

// colWidth is a single column's claim on the reservation pass.
func colWidth(c Column) int {
	if c.Weight > 0 {
		return max(0, c.Min)
	}
	return max(max(0, c.Min), max(0, c.Content))
}

// Scroll is a page's horizontal scroll state: which column the window
// starts at, and the frame width the keys measure against. It lives
// beside Window because the clamp below is a rule about WindowAt, not
// about any one page.
type Scroll struct {
	// Offset is how many columns the row is scrolled past the pinned
	// first one.
	Offset int
	// Width is the width of the last painted frame. No width reaches a
	// page at key time, so the keys read it back to tell a clipped row
	// from one that fits.
	Width int
}

// Left moves the window back by one column.
func (s *Scroll) Left(win Window) {
	s.Offset = max(0, s.clamped(win)-1)
}

// Right moves the window on by one column. Only a clipped row
// scrolls, so the key is dead on a terminal that shows everything.
func (s *Scroll) Right(win Window) {
	if win.ClipRight {
		s.Offset = s.clamped(win) + 1
	}
}

// clamped pulls a stale offset back to the last one that still moves
// the window. WindowAt saturates past that, so an unclamped counter
// leaves Left dead for as many presses as the operator made.
func (s *Scroll) clamped(win Window) int {
	return min(s.Offset, max(0, win.Total-2))
}

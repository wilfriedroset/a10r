// SPDX-License-Identifier: Apache-2.0

package table

import (
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

const (
	colSep         = " "
	colSepCells    = len(colSep)
	sortArrowCells = 2
)

func headerWidth(c Column) int {
	w := lipgloss.Width(c.Title)
	if c.Sortable {
		w += sortArrowCells
	}
	return w
}

// specs turns the column set into allocator columns, raising every
// floor to the header width so no page can forget the rule. A
// configured width therefore bounds the cells, not the column.
func specs(cols []Column) []format.Column {
	out := make([]format.Column, len(cols))
	for i, c := range cols {
		floor := max(c.Min, headerWidth(c))
		out[i] = format.Column{Min: floor, Content: max(floor, c.Content), Weight: c.Weight}
	}
	return out
}

// Layout is one frame's answer: the columns in view, their widths,
// and which edge is clipped. format.Distribute returns one width per
// window entry, so the two slices index together and the painters
// need no length guard.
type Layout struct {
	cols   []Column
	win    format.Window
	widths []int
}

// Layout resolves the pending key intent against width, then picks
// the window and the per-column widths for the frame being painted.
// Width flows one way, from View into the module: the key handler
// banks a direction and never reads a budget, so no frame is laid out
// against a width the operator is no longer looking at.
func (s *Scroll) Layout(cols []Column, width int) Layout {
	sp := specs(cols)
	budget := max(0, width-format.RowPrefixCols)
	s.resolve(sp, budget)
	win, budget := window(sp, budget, s.Offset)
	shown := make([]format.Column, len(win.Cols))
	for i, ci := range win.Cols {
		shown[i] = sp[ci]
	}
	return Layout{cols: cols, win: win, widths: format.Distribute(shown, budget, colSepCells)}
}

// window places the horizontal window and returns the budget left for
// the columns in it. A clipped row keeps one cell out of that budget
// for the ">" marker, so the header never runs past the body and
// wraps. Re-running on the smaller budget can only drop a further
// column, never bring one back, so the result is stable.
func window(sp []format.Column, budget, offset int) (win format.Window, inner int) {
	inner = budget
	win = format.WindowAt(sp, inner, colSepCells, offset)
	if len(win.Cols) < win.Total {
		inner = max(0, inner-1)
		win = format.WindowAt(sp, inner, colSepCells, offset)
	}
	return win, inner
}

// WidthOf returns the cells the column named key was given, or 0 when
// the horizontal window has scrolled it out of view. A page that must
// clip a cell itself asks here rather than counting positions.
func (l Layout) WidthOf(key string) int {
	for j, ci := range l.win.Cols {
		if l.cols[ci].Key == key {
			return l.widths[j]
		}
	}
	return 0
}

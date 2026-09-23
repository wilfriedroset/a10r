// SPDX-License-Identifier: Apache-2.0

package table

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// Header paints the column-title row, tinting the active sort column
// and appending its arrow.
func (l Layout) Header(sort Sort, chrome Chrome) string {
	var b strings.Builder
	b.WriteString(format.ScrollPrefix(l.win.ClipLeft))
	for j, ci := range l.win.Cols {
		if j > 0 {
			b.WriteString(colSep)
		}
		c := l.cols[ci]
		title := c.Title
		if arrow := sort.arrowFor(c.Key); arrow != "" {
			title += " " + arrow
		}
		padded := format.PadRight(title, l.widths[j])
		if sort.isActive(c.Key) {
			b.WriteString(chrome.ActiveFg.Render(padded))
		} else {
			b.WriteString(chrome.Fg.Render(padded))
		}
	}
	if l.win.ClipRight {
		b.WriteString(format.ScrollRightMarker)
	}
	return b.String()
}

// Row paints one data line, padded to width and wrapped in the row's
// style. A row with fewer cells than the column set renders the tail
// columns empty, so a page never needs a length guard.
func (l Layout) Row(r Row, width int) string {
	var b strings.Builder
	// The line is at least width bytes and more once a cell carries
	// SGR, so one sized allocation replaces the Builder's regrowth.
	b.Grow(width * 2)
	b.WriteString(r.Prefix)
	for j, ci := range l.win.Cols {
		if j > 0 {
			b.WriteString(colSep)
		}
		b.WriteString(paintCell(cellAt(r.Cells, ci), l.cols[ci].Clip, l.widths[j]))
	}
	return r.Style.Render(format.PadRight(b.String(), width))
}

func cellAt(cells []Cell, i int) Cell {
	if i < len(cells) {
		return cells[i]
	}
	return Cell{}
}

// paintCell clips, paints, then pads, in that order. Painting the pad
// would let a trailing match underline the column gap. Cutting after
// the paint is worse: the cut runs through the escapes the paint
// wrote, and closing them costs a reset mid-line that ends the
// row-level style for every column after it.
func paintCell(c Cell, clip ClipMode, w int) string {
	shown := c.Text
	if clip == ClipEllipsis {
		shown = format.Ellipsize(shown, w)
	} else {
		shown = format.SGRTruncate(shown, w)
	}
	if c.Paint != nil {
		shown = c.Paint(shown)
	}
	return format.PadRight(shown, w)
}

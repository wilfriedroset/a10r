// SPDX-License-Identifier: Apache-2.0

package table

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// Header paints the column-title row, tinting the active sort column
// and appending its arrow.
func (l Layout) Header(sort Sort, chrome Chrome) string {
	var b strings.Builder
	b.WriteString(scrollPrefix(l.win.ClipLeft))
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
		b.WriteString(scrollRightMarker)
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
	switch clip {
	case ClipEllipsis:
		shown = format.Ellipsize(shown, w)
	case ClipMiddle:
		shown = clipMiddle(shown, w)
	default:
		shown = format.SGRTruncate(shown, w)
	}
	if c.Paint != nil {
		shown = c.Paint(shown)
	}
	return format.PadRight(shown, w)
}

// clipMiddle keeps both the head and the discriminating tail of s,
// spending one cell on the ellipsis between them. Below the ellipsis
// there is no middle to split, so the cut falls back to the tail
// form.
func clipMiddle(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	suffix := lipgloss.Width(format.EllipsizeSuffix)
	if w <= suffix {
		return format.Ellipsize(s, w)
	}
	keep := w - suffix
	head := (keep + 1) / 2
	return format.Truncate(s, head) + format.EllipsizeSuffix + truncateLeft(s, keep-head)
}

// truncateLeft returns the suffix of s at most w cells wide, walking
// runes from the end. Mirrors format.Truncate from the other side.
func truncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	runes := []rune(s)
	used := 0
	cut := len(runes)
	for i, r := range slices.Backward(runes) {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		used += rw
		cut = i
	}
	return string(runes[cut:])
}

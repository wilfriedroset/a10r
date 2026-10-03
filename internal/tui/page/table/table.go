// SPDX-License-Identifier: Apache-2.0

// Package table paints a list table from one ordered column set and
// one ordered row set. Every positional question lives here: which
// columns the frame has room for, how wide each one is, where the
// horizontal window starts, and which edge is clipped. A page names
// its columns once, in order, and never counts positions again.
//
// The layout pass raises every column's floor to its own header,
// including the cells a sort arrow needs. The rule is a step of the
// pass rather than a function a page calls, because a header cut down
// to "CLU" with no arrow lies about both the column and the live sort
// direction, and the arrow is the whole direction contract (ADR 0048).
package table

import (
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
)

// Column is one table column: its identity, its header, and how it
// claims width from the allocator. Min, Content and Weight carry the
// format.Distribute meanings; Min is raised to the header floor
// before the allocator sees it.
type Column struct {
	// Key is the sorter key and the column's identity. The header's
	// arrow lookup and Layout.WidthOf both address a column by it.
	Key   string
	Title string
	// Sortable reserves the arrow cells whether or not the column is
	// the active sort, so landing the sort on it never cuts the title.
	Sortable bool
	Min      int
	Content  int
	Weight   int
	Clip     ClipMode
}

// ClipMode is what a cell does when its text outgrows the width the
// allocator gave its column.
type ClipMode int

const (
	// ClipPad cuts at the column edge with no marker. Measured
	// built-in columns take this: they overflow only once the
	// allocator is already under every floor, where an ellipsis would
	// spend the last content cell on saying so.
	ClipPad ClipMode = iota
	// ClipEllipsis marks the cut, because a silent slice of a label
	// value reads as a different value. Plain text only: the cut is
	// not SGR-aware, so a cell its producer already coloured takes
	// ClipPad.
	ClipEllipsis
	// ClipMiddle cuts the middle out instead of the tail. Two values
	// sharing a long prefix and differing at the end (`…-1a-0042` vs
	// `…-1b-0117`) stay distinguishable, where a tail cut collapses
	// them to the same string. Plain text only, on the same terms as
	// ClipEllipsis.
	ClipMiddle
)

// Cell is one rendered value plus the optional paint the page applies
// to it.
type Cell struct {
	Text string
	// Paint runs on the clipped text, before the pad, so the trailing
	// pad stays unstyled. It must not change the rendered width.
	Paint func(shown string) string
}

// Row is one painted line: the cursor and mark glyphs, one cell per
// column in the column set's order, and the row-level style.
type Row struct {
	// Prefix is the cursor and mark glyphs. It must render
	// format.RowPrefixCols wide: the layout takes those cells out of
	// the column budget, so a shorter or longer one shifts the row off
	// the header.
	Prefix string
	Cells  []Cell
	// Style wraps the whole padded line. The zero value leaves the
	// line untouched.
	Style lipgloss.Style
}

// Sort is the header's read-only view of the live sort. Either field
// may be nil, which renders every column plain.
type Sort struct {
	Arrow  func(key string) string
	Active func(key string) bool
}

func (s Sort) arrowFor(key string) string {
	if s.Arrow == nil {
		return ""
	}
	return s.Arrow(key)
}

func (s Sort) isActive(key string) bool {
	return s.Active != nil && s.Active(key)
}

// Chrome is the header's two foreground renderers: the active sort
// column takes ActiveFg, every other column takes Fg.
type Chrome struct {
	Fg       lipgloss.Style
	ActiveFg lipgloss.Style
}

// SortAxes orders axes as cols renders them and keeps only the
// sortable columns, so the sorter's h/l walk is the header read left
// to right. An axis with no sortable column is dropped.
func SortAxes[T any](cols []Column, axes []tablesort.Column[T]) []tablesort.Column[T] {
	byKey := make(map[string]tablesort.Column[T], len(axes))
	for _, a := range axes {
		byKey[a.Key] = a
	}
	out := make([]tablesort.Column[T], 0, len(axes))
	for _, c := range cols {
		if a, ok := byKey[c.Key]; ok && c.Sortable {
			out = append(out, a)
		}
	}
	return out
}

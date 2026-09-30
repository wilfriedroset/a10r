// SPDX-License-Identifier: Apache-2.0

package table

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/config"
)

// LabelColumn is one config.Column resolved for rendering: the parts
// a page needs to turn `pages.*.columns` into a Column and to sort on
// it.
type LabelColumn struct {
	Label  string
	Title  string
	Key    string
	Hotkey rune
	Width  int
	Wide   bool
	// Index is the column's position in the declared config order. A
	// row's cell slice and the sorter's axes are both keyed by it, so
	// a page that renders only some of the columns still reads the
	// right cell for each one.
	Index int
}

// keyPrefix namespaces a user column's sort key so it can never
// collide with a built-in key ("age", "severity") in the sorter's
// column list or in the remembered-sort state file.
const keyPrefix = "label:"

// Resolve materialises the rendering view of the configured columns.
// The config loader has already validated every field, so this
// function does not re-check them.
func Resolve(cols []config.Column) []LabelColumn {
	if len(cols) == 0 {
		return nil
	}
	out := make([]LabelColumn, 0, len(cols))
	for i, c := range cols {
		rc := LabelColumn{
			Label: c.Label,
			Title: c.TitleOrDefault(),
			Key:   keyPrefix + c.Label,
			Width: c.Width,
			Wide:  c.Wide,
			Index: i,
		}
		if c.SortKey != "" {
			rc.Hotkey = rune(c.SortKey[0])
		}
		out = append(out, rc)
	}
	return out
}

// LabelSet is a page's user-declared label columns together with the
// display tier. A wide column stays out of view until the operator
// presses Shift+W, which is the answer to a terminal too narrow for
// every column the operator wants available (ADR 0048).
type LabelSet struct {
	all    []LabelColumn
	shown  []LabelColumn
	widths []int
	wide   bool
}

// NewLabelSet resolves cols and starts at the narrow tier.
func NewLabelSet(cols []config.Column) LabelSet {
	var s LabelSet
	s.Reconfigure(cols)
	return s
}

// Reconfigure re-resolves the columns after a reload. The wide tier
// survives only while a wide column does, so a later reload that adds
// one back opens it hidden.
func (s *LabelSet) Reconfigure(cols []config.Column) {
	s.all = Resolve(cols)
	if !s.HasWide() {
		s.wide = false
	}
	s.shown = s.visible()
}

// ToggleWide flips the display tier. It reports false when no column
// is wide, which spares the caller a recompute that would paint an
// identical frame.
func (s *LabelSet) ToggleWide() bool {
	if !s.HasWide() {
		return false
	}
	s.wide = !s.wide
	s.shown = s.visible()
	return true
}

// All returns every declared column, in config order. Row cell slices
// are built from it, so a tier change never rebuilds them.
func (s *LabelSet) All() []LabelColumn { return s.all }

// Shown hides the wide columns until Shift+W.
func (s *LabelSet) Shown() []LabelColumn { return s.shown }

// HasWide reports whether any column is wide. A page with none must
// not advertise Shift+W, because the key would change nothing.
func (s *LabelSet) HasWide() bool {
	for _, c := range s.all {
		if c.Wide {
			return true
		}
	}
	return false
}

// HiddenSortKey reports a sort key the operator cannot see right
// now, which is a wide column outside the wide tier. Pages install it
// on the sorter so h/l steps over the axis and its hotkey stays dead.
func (s *LabelSet) HiddenSortKey(key string) bool {
	for _, c := range s.all {
		if c.Key == key {
			return c.Wide && !s.wide
		}
	}
	return false
}

// Cells reads one value per declared column, in config order. It
// returns nil when none is declared, so a page without columns
// allocates nothing per row.
func (s *LabelSet) Cells(value func(label string) string) []string {
	if len(s.all) == 0 {
		return nil
	}
	out := make([]string, len(s.all))
	for i, c := range s.all {
		out[i] = value(c.Label)
	}
	return out
}

// Measure refreshes the widths Columns reads. A page calls it after
// every row or tier change, never per frame (see MeasureLabels).
func (s *LabelSet) Measure(rows int, cell func(r, i int) string) {
	s.widths = MeasureLabels(s.shown, rows, cell)
}

// Columns is the shown block at the last measured widths.
func (s *LabelSet) Columns() []Column { return LabelColumns(s.shown, s.widths) }

func (s *LabelSet) visible() []LabelColumn {
	if s.wide || !s.HasWide() {
		return s.all
	}
	out := make([]LabelColumn, 0, len(s.all))
	for _, c := range s.all {
		if !c.Wide {
			out = append(out, c)
		}
	}
	return out
}

// labelWidthFloor is the narrowest a measured label column asks for
// before its own header floor is applied. Below this a value is an
// ellipsis and a character or two, which says less than an empty cell
// would.
const labelWidthFloor = 6

// MeasureLabels measures each column over all rows, so a vertical
// scroll never shifts a width. cell returns row r's value for the
// column at config index i. A column with a configured Width needs no
// measuring and is left at zero, because LabelColumns pins it before
// it reads the slice. The scan is O(rows x columns), so a page calls
// this once per row change rather than once per frame.
func MeasureLabels(cols []LabelColumn, rows int, cell func(r, i int) string) []int {
	if len(cols) == 0 {
		return nil
	}
	out := make([]int, len(cols))
	for i, c := range cols {
		if c.Width > 0 {
			continue
		}
		for r := range rows {
			out[i] = max(out[i], lipgloss.Width(cell(r, c.Index)))
		}
	}
	return out
}

// LabelColumns turns the measured widths into the user-declared
// block. A measured column flexes rather than reserving its full
// width: label values run long (a pod name, an instance URL), and a
// weight-0 request that wide pushes the allocator into its
// proportional shrink, which takes the built-in columns below their
// own floors. Flexing reserves only the floor and grows into what is
// left alongside the page's flex column, so a long value ellipsizes
// instead of collapsing the row. A configured width pins the cells;
// the layout pass still floors the column at its own header
// (ADR 0048).
func LabelColumns(cols []LabelColumn, widths []int) []Column {
	out := make([]Column, 0, len(cols))
	for i, c := range cols {
		col := Column{Key: c.Key, Title: c.Title, Sortable: c.Hotkey != 0, Clip: ClipEllipsis}
		if c.Width > 0 {
			col.Min, col.Content = c.Width, c.Width
		} else {
			w := 0
			if i < len(widths) {
				w = widths[i]
			}
			col.Min, col.Content, col.Weight = min(labelWidthFloor, w), w, 1
		}
		out = append(out, col)
	}
	return out
}

// RollupMarker is the cell a page renders when the rows behind one
// aggregated cell disagree, counting the distinct values instead of
// picking one of them.
func RollupMarker(distinct int) string { return fmt.Sprintf(markerFormat, distinct) }

// markerPrefix and markerSuffix bracket the marker: CellLess matches
// on both ends so a real label value of "<unset>" stays a plain
// value.
const (
	markerFormat = "<%d values>"
	markerPrefix = "<"
	markerSuffix = " values>"
)

// CellLess is the ascending comparator for two rendered cells. Rollup
// markers rank after every plain value rather than landing wherever
// their leading "<" falls in byte order.
func CellLess(a, b string) bool {
	ra, rb := rank(a), rank(b)
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func rank(v string) int {
	if strings.HasPrefix(v, markerPrefix) && strings.HasSuffix(v, markerSuffix) {
		return 1
	}
	return 0
}

// CellEmpty reports a cell with no value. The sorter pins these after
// every other row in both directions: an unknown label is not the
// smallest label.
func CellEmpty(v string) bool { return v == "" }

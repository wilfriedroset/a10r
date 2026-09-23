// SPDX-License-Identifier: Apache-2.0

// Package labelcol turns the user's `pages.*.columns` config into the
// values a table page renders and sorts on. It holds the parts the
// alerts page and the group-detail page must agree on — the header
// title and the cells it needs, the sort key, the aggregate rollup
// rule, and the ordering of rollup markers and empty cells. Each page
// keeps its own layout; only the header's own width lives here,
// because getting it wrong drops the sort arrow identically on both.
// See ADR 0048.
package labelcol

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// Column is one config.Column resolved for rendering.
type Column struct {
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

// HeaderWidth is the cells a column's header needs. A sortable column
// reserves two more than its title, because the renderer appends
// " <arrow>" when the column is the active sort. Without the reserve
// a column whose widest cell is narrower than its title measures too
// small and the arrow is truncated away, which is the only thing that
// tells ASC from DESC.
func HeaderWidth(c Column) int {
	w := lipgloss.Width(c.Title)
	if c.Hotkey != 0 {
		w += sortArrowCells
	}
	return w
}

// sortArrowCells is the width of the " <arrow>" the header renderer
// appends to the active sort column.
const sortArrowCells = 2

// Floor is the narrowest the allocator is asked to make c, given the
// width want the caller would otherwise ask for. A column is never
// narrower than its own header: a cut title with no arrow lies about
// both the column's identity and the live sort direction, and the
// arrow is the whole direction contract (ADR 0048). A configured
// `width` therefore sizes the cells, not the column.
func Floor(c Column, want int) int {
	return max(want, HeaderWidth(c))
}

// Resolve materialises the rendering view of the configured columns.
// The config loader has already validated every field, so this
// function does not re-check them.
func Resolve(cols []config.Column) []Column {
	if len(cols) == 0 {
		return nil
	}
	out := make([]Column, 0, len(cols))
	for i, c := range cols {
		rc := Column{
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

// Visible returns the columns a page renders at the current tier. A
// wide column stays out of view until the operator presses Shift+W,
// which is the answer to a terminal too narrow for every column the
// operator wants available (ADR 0048).
func Visible(cols []Column, wide bool) []Column {
	if wide || !HasWide(cols) {
		return cols
	}
	out := make([]Column, 0, len(cols))
	for _, c := range cols {
		if !c.Wide {
			out = append(out, c)
		}
	}
	return out
}

// HasWide reports whether any column is wide. A page with none must
// not advertise Shift+W, because the key would change nothing.
func HasWide(cols []Column) bool {
	for _, c := range cols {
		if c.Wide {
			return true
		}
	}
	return false
}

// AggregateCell rolls a label up over every instance of an alertname
// aggregate: the shared value when all agree, a distinct-count marker
// when they do not. An instance missing the label contributes the
// empty value, which counts as one distinct value — a half-populated
// label is disagreement, not absence.
func AggregateCell(instances []backend.Alert, label string) string {
	if len(instances) == 0 {
		return ""
	}
	first := instances[0].Labels[label]
	// The set is built only once a value disagrees. Most rows agree,
	// and a map per group per column costs more than the scan does.
	var seen map[string]struct{}
	for _, a := range instances[1:] {
		v := a.Labels[label]
		if seen == nil {
			if v == first {
				continue
			}
			seen = map[string]struct{}{first: {}}
		}
		seen[v] = struct{}{}
	}
	if seen == nil {
		return first
	}
	return fmt.Sprintf(markerFormat, len(seen))
}

// markerFormat renders the distinct-count marker. markerPrefix and
// markerSuffix bracket it: rank matches on both ends so a real label
// value of "<unset>" stays a plain value.
const (
	markerFormat = "<%d values>"
	markerPrefix = "<"
	markerSuffix = " values>"
)

// Less is the ascending comparator for two rendered cells. Rollup
// markers rank after every plain value rather than landing wherever
// their leading "<" falls in byte order.
func Less(a, b string) bool {
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

// IsEmpty reports a cell with no value. The sorter pins these after
// every other row in both directions: an unknown label is not the
// smallest label.
func IsEmpty(v string) bool { return v == "" }

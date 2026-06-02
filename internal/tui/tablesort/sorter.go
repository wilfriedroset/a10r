// SPDX-License-Identifier: Apache-2.0

// Package tablesort holds the shared sort-state machine for list /
// table pages. Each page contributes a slice of Column[T] (key,
// title, hotkey, default direction, comparator) and a Sorter[T]
// owns the convention: Shift+<letter> chooses a column, repeating
// the active column flips ASC↔DESC, h/l walks columns left/right
// in registration order with wrap-around, and switching to a new
// column resets to that column's default direction. Header arrow
// glyphs and Bindings() entries for the help registry come from
// the same Sorter so every page renders the convention identically.
//
// The package is deliberately layout-agnostic: pages still own
// their own column widths and renderHeader paint logic. Sorter
// only contributes the active-column key (so the page can apply
// theme.Table.HeaderActive), the arrow glyph for a given column,
// and the Bindings list for help.
package tablesort

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/wilfriedroset/a10r/internal/tui/action"
)

// Column describes one sortable axis on a table page. Less is the
// strict ASCENDING comparator — Apply flips arguments when DESC.
// Less takes pointers so the comparator does not copy each entry on
// every comparison; for entry types holding label maps and slices
// (alerts, silences) the per-cmp copy ran into hundreds of bytes
// and dominated sort cost on large views.
//
// Hotkey is the uppercase rune that selects the column. Zero means
// "no shift+letter shortcut for this column" — the column is still
// reachable via h/l walking and SelectByKey, but Bindings() omits
// it from the help registry. Tenant pages that want a column
// reachable without a help-visible shortcut can use this.
type Column[T any] struct {
	Key        string
	Title      string
	Hotkey     rune
	DefaultAsc bool
	Less       func(a, b *T) bool
	// Description overrides the help-overlay text for this column's
	// hotkey. Empty falls back to "sort by <lowercased title>" —
	// most pages get the right description for free, but a column
	// whose header label and natural English name diverge (e.g.,
	// header "COUNT" but description "sort by alert count") can
	// supply both without forcing the title to spell out the
	// description.
	Description string
}

// Sorter is the per-page sort-state machine. Construct via New;
// the zero value is not usable.
type Sorter[T any] struct {
	cols       []Column[T]
	active     int
	asc        bool
	defaultIdx int
}

// New constructs a Sorter over the supplied columns. defaultKey
// names the column to start active; an unknown key falls back to
// the first column. Panics on zero columns or any column with a
// nil Less — both are programmer errors caught at startup rather
// than allowed to surface as a runtime nil-deref deeper in.
func New[T any](cols []Column[T], defaultKey string) *Sorter[T] {
	if len(cols) == 0 {
		panic(errors.New("tablesort.New: empty columns"))
	}
	for _, c := range cols {
		if c.Less == nil {
			panic(fmt.Errorf("tablesort.New: column %q has nil Less", c.Key))
		}
	}
	s := &Sorter[T]{cols: cols}
	if i := indexOf(cols, defaultKey); i >= 0 {
		s.active = i
	}
	// defaultIdx pins the New-time resolved index so Reset can
	// return here regardless of subsequent SelectBy/Walk
	// transitions; the resolved index is the source of truth
	// because defaultKey may have been unknown and silently
	// fallen back to 0.
	s.defaultIdx = s.active
	s.asc = cols[s.active].DefaultAsc
	return s
}

// Apply sorts in place by the active column and direction using
// sort.SliceStable so tied entries preserve their input order.
// Tied-entry stability is what keeps the cursor on the same row
// content across consecutive Apply calls when nothing else changed.
func (s *Sorter[T]) Apply(in []T) {
	less := s.cols[s.active].Less
	asc := s.asc
	sort.SliceStable(in, func(i, j int) bool {
		if asc {
			return less(&in[i], &in[j])
		}
		return less(&in[j], &in[i])
	})
}

// ActiveKey returns the active column's stable Key.
func (s *Sorter[T]) ActiveKey() string { return s.cols[s.active].Key }

// Asc reports the active direction.
func (s *Sorter[T]) Asc() bool { return s.asc }

// SelectByHotkey switches to the column whose Hotkey matches r.
// Same column twice flips ASC↔DESC; switching to a new column
// resets to that column's default direction. Returns true when
// the hotkey matched a column.
func (s *Sorter[T]) SelectByHotkey(r rune) bool {
	for i, c := range s.cols {
		if c.Hotkey != 0 && c.Hotkey == r {
			s.selectIndex(i)
			return true
		}
	}
	return false
}

// SelectByKey switches to the column whose Key matches. Same flip
// rules as SelectByHotkey. Returns true when the key matched.
func (s *Sorter[T]) SelectByKey(key string) bool {
	if i := indexOf(s.cols, key); i >= 0 {
		s.selectIndex(i)
		return true
	}
	return false
}

// Reset returns the Sorter to the column and direction it was
// constructed with — the New-time defaultKey resolved to its
// column index, or column 0 if unknown, and that column's
// DefaultAsc. Does NOT consult `cols[defaultIdx].DefaultAsc` from
// any subsequent column-mutation path because Sorter has none;
// the field is fixed at construction.
//
// Bound to `-` per the user-visible keybindings doc.
func (s *Sorter[T]) Reset() {
	s.active = s.defaultIdx
	s.asc = s.cols[s.defaultIdx].DefaultAsc
}

// selectIndex is the shared transition: same-column flips, new
// column resets to the column's DefaultAsc.
func (s *Sorter[T]) selectIndex(i int) {
	if s.active == i {
		s.asc = !s.asc
		return
	}
	s.active = i
	s.asc = s.cols[i].DefaultAsc
}

// WalkRight selects the next column in registration order, wrapping
// from the last back to the first. Direction resets to the new
// column's default — walking is conceptually the same intent as
// SelectByHotkey('X') for a fresh column. Returns false (no-op)
// when there is only one column.
func (s *Sorter[T]) WalkRight() bool {
	if len(s.cols) < 2 {
		return false
	}
	next := (s.active + 1) % len(s.cols)
	s.active = next
	s.asc = s.cols[next].DefaultAsc
	return true
}

// WalkLeft is the symmetric inverse of WalkRight.
func (s *Sorter[T]) WalkLeft() bool {
	if len(s.cols) < 2 {
		return false
	}
	prev := s.active - 1
	if prev < 0 {
		prev = len(s.cols) - 1
	}
	s.active = prev
	s.asc = s.cols[prev].DefaultAsc
	return true
}

// HandleKey is the convenience dispatcher: it routes "h"/"left",
// "l"/"right", a bare uppercase letter ("S") and the long form
// ("shift+s") through the appropriate primitive. Returns true when
// the key was consumed (state changed). Pages call HandleKey from
// their key handler and then trigger their own recompute / Apply.
//
// bubbletea v2's KeyPressMsg.String() emits both uppercase-letter
// and "shift+letter" forms depending on context, so the helper
// accepts either rather than forcing every page to translate.
func (s *Sorter[T]) HandleKey(key string) bool {
	switch key {
	case "h", "left":
		return s.WalkLeft()
	case "l", "right":
		return s.WalkRight()
	case "-":
		s.Reset()
		return true
	}
	if r, ok := singleUpperRune(key); ok {
		return s.SelectByHotkey(r)
	}
	if rest, ok := strings.CutPrefix(key, "shift+"); ok {
		if r, ok := singleUpperRune(strings.ToUpper(rest)); ok {
			return s.SelectByHotkey(r)
		}
	}
	return false
}

// ArrowFor returns the sort indicator glyph for the column whose
// Key matches: "↑" or "↓" when active, "" otherwise. Pages embed
// this in their renderHeader output adjacent to the column title.
func (s *Sorter[T]) ArrowFor(key string) string {
	if s.cols[s.active].Key != key {
		return ""
	}
	if s.asc {
		return "↑"
	}
	return "↓"
}

// IsActive reports whether the named column is the active sort
// column. Pages branch on this when applying theme.Table.HeaderActive
// to the active column header.
func (s *Sorter[T]) IsActive(key string) bool {
	return s.cols[s.active].Key == key
}

// Bindings returns one action.Action per column with a non-zero
// Hotkey plus a single "-" entry advertising Reset. Ready to
// register with the supplied view name; the help overlay then
// reads uniformly across pages without each page hand-rolling
// its own description string.
func (s *Sorter[T]) Bindings(view string) []action.Action {
	out := make([]action.Action, 0, len(s.cols)+1)
	for _, c := range s.cols {
		if c.Hotkey == 0 {
			continue
		}
		desc := c.Description
		if desc == "" {
			desc = "sort by " + strings.ToLower(c.Title)
		}
		out = append(out, action.Action{
			Key:         "Shift+" + string(c.Hotkey),
			Description: desc,
			View:        view,
		})
	}
	out = append(out, action.Action{
		Key:         "-",
		Description: "reset sort to default",
		View:        view,
	})
	return out
}

// indexOf returns the column index whose Key matches, or -1.
func indexOf[T any](cols []Column[T], key string) int {
	for i, c := range cols {
		if c.Key == key {
			return i
		}
	}
	return -1
}

// singleUpperRune reports whether s is exactly one ASCII uppercase
// letter and returns the rune. Used by HandleKey to recognise the
// bare-letter shift-letter form bubbletea v2 emits.
func singleUpperRune(s string) (rune, bool) {
	if len(s) != 1 {
		return 0, false
	}
	r := rune(s[0])
	if r < 'A' || r > 'Z' {
		return 0, false
	}
	return r, true
}

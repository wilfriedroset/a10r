// SPDX-License-Identifier: Apache-2.0

package tablesort_test

import (
	"slices"
	"testing"

	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
)

const keyExtra = "extra"

// tierCols is fixtureCols plus a third column the page can hide, so
// the tests can walk over a hidden column and land past it.
func tierCols() []tablesort.Column[row] {
	return append(fixtureCols(), tablesort.Column[row]{
		Key: keyExtra, Title: "EXTRA", Hotkey: 'X', DefaultAsc: true,
		Less: func(a, b *row) bool { return a.Name > b.Name },
	})
}

// hideExtra returns a sorter over tierCols with EXTRA hidden.
func hideExtra(t *testing.T) *tablesort.Sorter[row] {
	t.Helper()
	s := tablesort.New(tierCols(), keyName)
	s.SetHidden(func(key string) bool { return key == keyExtra })
	return s
}

func TestHidden_WalkSkipsAHiddenColumn(t *testing.T) {
	t.Parallel()
	s := hideExtra(t)

	s.WalkRight()
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("WalkRight = %q, want %q", got, keyScore)
	}
	s.WalkRight()
	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("WalkRight past the hidden column = %q, want %q", got, keyName)
	}
	s.WalkLeft()
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("WalkLeft past the hidden column = %q, want %q", got, keyScore)
	}
}

// A walk that starts while the sort is parked has to step on from the
// column the operator sees, not from the hidden choice. EXTRA sits
// last, so walking from it wraps to NAME while walking from the
// parked NAME lands on SCORE.
func TestHidden_WalkStartsFromTheParkedColumn(t *testing.T) {
	t.Parallel()
	wide := true
	s := tablesort.New(tierCols(), keyName)
	s.SetHidden(func(key string) bool { return key == keyExtra && !wide })
	if !s.HandleKey("X") {
		t.Fatal("Shift+X must select a visible column")
	}
	wide = false

	s.WalkRight()
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("WalkRight while parked = %q, want %q", got, keyScore)
	}
}

// A hotkey for a column the operator cannot see is not consumed, so
// the page does not redraw an identical frame and no state file
// records a sort on an invisible column.
func TestHidden_HotkeyDoesNotSelectAHiddenColumn(t *testing.T) {
	t.Parallel()
	s := hideExtra(t)

	if s.HandleKey("X") {
		t.Fatal("Shift+X selected a hidden column")
	}
	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("ActiveKey = %q, want %q", got, keyName)
	}
	if s.SelectByKey(keyExtra) {
		t.Fatal("SelectByKey selected a hidden column")
	}
}

// Hiding the active column parks the sort rather than losing it:
// the rows fall back to the default column, and un-hiding restores
// the operator's choice with its direction intact.
func TestHidden_ActiveColumnParksAndComesBack(t *testing.T) {
	t.Parallel()
	wide := true
	s := tablesort.New(tierCols(), keyName)
	s.SetHidden(func(key string) bool { return key == keyExtra && !wide })

	if !s.HandleKey("X") {
		t.Fatal("Shift+X must select a visible column")
	}
	wide = false

	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("a hidden active column must fall back to the default, got %q", got)
	}
	if s.ArrowFor(keyExtra) != "" {
		t.Fatal("a hidden column must not paint an arrow")
	}
	if !s.IsActive(keyName) {
		t.Fatal("the default column must read as active while the choice is parked")
	}

	wide = true
	if got := s.ActiveKey(); got != keyExtra {
		t.Fatalf("un-hiding must restore the parked column, got %q", got)
	}
}

// The help overlay lists what the keys do now. A hidden column's
// hotkey does nothing, so advertising it would be wrong.
func TestHidden_BindingsOmitAHiddenColumn(t *testing.T) {
	t.Parallel()
	s := hideExtra(t)

	keys := make([]string, 0, 4)
	for _, b := range s.Bindings("alerts") {
		keys = append(keys, b.Key)
	}
	if slices.Contains(keys, "Shift+X") {
		t.Fatalf("Bindings advertised a hidden column: %v", keys)
	}
	if !slices.Contains(keys, "Shift+N") {
		t.Fatalf("Bindings dropped a visible column: %v", keys)
	}
}

// An unset predicate is the state every other page is in, so none of
// them changes behaviour.
func TestHidden_UnsetPredicateChangesNothing(t *testing.T) {
	t.Parallel()
	s := tablesort.New(tierCols(), keyName)

	s.WalkLeft()
	if got := s.ActiveKey(); got != keyExtra {
		t.Fatalf("WalkLeft = %q, want %q", got, keyExtra)
	}
	if !s.IsActive(keyExtra) {
		t.Fatal("the walked-to column must be active")
	}
}

// Parking must move the direction with the column. The default
// column here reads DESC and the hidden one ASC, so a parked sort
// that kept the operator's direction would render the default column
// backwards -- on the alerts page that is least-severe-first.
func TestHidden_ParkedSortTakesTheDefaultDirection(t *testing.T) {
	t.Parallel()
	wide := true
	s := tablesort.New(tierCols(), keyScore)
	s.SetHidden(func(key string) bool { return key == keyExtra && !wide })

	if !s.HandleKey("X") {
		t.Fatal("Shift+X must select a visible column")
	}
	wide = false

	if s.Asc() {
		t.Fatal("a parked sort must read the default column's own direction")
	}
	if got := s.ArrowFor(keyScore); got != "↓" {
		t.Fatalf("ArrowFor(score) = %q, want the default DESC arrow", got)
	}
	rows := []row{{Name: "a", Score: 1}, {Name: "b", Score: 9}}
	s.Apply(rows)
	if rows[0].Score != 9 {
		t.Fatalf("parked rows sorted ascending: %+v", rows)
	}

	wide = true
	if !s.Asc() {
		t.Fatal("un-hiding must restore the operator's own direction")
	}
}

// While a sort is parked, the visible column's own hotkey has to flip
// the arrow on screen. Comparing the press against the hidden choice
// instead would re-apply the direction already shown, which reads as
// a dead key.
func TestHidden_DefaultHotkeyFlipsWhileParked(t *testing.T) {
	t.Parallel()
	wide := true
	s := tablesort.New(tierCols(), keyScore)
	s.SetHidden(func(key string) bool { return key == keyExtra && !wide })
	if !s.HandleKey("X") {
		t.Fatal("Shift+X must select a visible column")
	}
	wide = false

	if !s.HandleKey("C") {
		t.Fatal("Shift+C must select the visible default column")
	}
	if !s.Asc() {
		t.Fatal("pressing the parked-on column must flip it to ASC")
	}
}

// A page opens with its wide tier closed, so a remembered sort on a
// wide column has to restore as a parked sort rather than be dropped.
// Bind assigns the column directly for this reason: routing it
// through SelectByKey, which refuses a hidden column, would lose the
// operator's sort on every launch.
func TestHidden_RememberedSortOnAHiddenColumnParks(t *testing.T) {
	t.Parallel()
	wide := false
	m := newFakeMemory(keyExtra + ":asc")
	s := tablesort.New(tierCols(), keyScore)
	s.Bind(m, resourceFixture)
	s.SetHidden(func(key string) bool { return key == keyExtra && !wide })

	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("a remembered hidden column must park, got %q", got)
	}
	wide = true
	if got := s.ActiveKey(); got != keyExtra {
		t.Fatalf("opening the tier must restore the remembered column, got %q", got)
	}
	if !s.Asc() {
		t.Fatal("the restored column must keep its remembered direction")
	}
}

// SPDX-License-Identifier: Apache-2.0

package tablesort_test

import (
	"slices"
	"testing"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
)

// row is a tiny entry type used to exercise the generic Sorter.
type row struct {
	Name  string
	Score int
}

// Column-key constants pulled out so goconst doesn't flag the
// fixture's repeated literals.
const (
	keyName  = "name"
	keyScore = "score"
)

// nameLess and scoreLess are the canonical ASC comparators for the
// fixture columns. Score is unsigned-style — higher score wins when
// the column defaults to DESC.
func nameLess(a, b row) bool  { return a.Name < b.Name }
func scoreLess(a, b row) bool { return a.Score < b.Score }

// fixtureCols returns a two-column setup: NAME (ASC default,
// hotkey N) and SCORE (DESC default, hotkey C). Two columns
// exercises both walk wrap-around and per-column default direction.
func fixtureCols() []tablesort.Column[row] {
	return []tablesort.Column[row]{
		{Key: keyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true, Less: nameLess},
		{Key: keyScore, Title: "SCORE", Hotkey: 'C', DefaultAsc: false, Less: scoreLess},
	}
}

func TestNewDefaultsToProvidedKey(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyScore)
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("ActiveKey = %q, want %q", got, keyScore)
	}
	if s.Asc() {
		t.Fatalf("Asc = true, want false (score column defaults DESC)")
	}
}

func TestNewFallsBackToFirstColumnWhenKeyUnknown(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), "bogus")
	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("ActiveKey = %q, want %q", got, keyName)
	}
	if !s.Asc() {
		t.Fatalf("Asc = false, want true (name column defaults ASC)")
	}
}

func TestNewPanicsOnEmptyColumns(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on empty columns")
		}
	}()
	tablesort.New[row](nil, "")
}

func TestNewPanicsOnNilLess(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on column with nil Less")
		}
	}()
	tablesort.New([]tablesort.Column[row]{
		{Key: "x", Title: "X", DefaultAsc: true},
	}, "x")
}

func TestApplyAscByName(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	in := []row{{Name: "c"}, {Name: "a"}, {Name: "b"}}
	s.Apply(in)
	got := []string{in[0].Name, in[1].Name, in[2].Name}
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyDescByScoreDefault(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyScore)
	in := []row{{Score: 1}, {Score: 5}, {Score: 3}}
	s.Apply(in)
	got := []int{in[0].Score, in[1].Score, in[2].Score}
	if want := []int{5, 3, 1}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestApplyIsStable(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	in := []row{
		{Name: "a", Score: 1},
		{Name: "a", Score: 2}, // tied on Name → preserves input order
		{Name: "a", Score: 3},
	}
	s.Apply(in)
	got := []int{in[0].Score, in[1].Score, in[2].Score}
	if want := []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("stable sort lost insertion order on ties: got %v, want %v", got, want)
	}
}

func TestApplyEmptyAndSingle(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	s.Apply([]row(nil)) // must not panic
	one := []row{{Name: "x"}}
	s.Apply(one)
	if one[0].Name != "x" {
		t.Fatalf("single-element apply mutated content: %v", one)
	}
}

func TestSelectByHotkeyFlipsOnRepeat(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	if !s.SelectByHotkey('N') {
		t.Fatalf("SelectByHotkey('N') = false, want true")
	}
	// First press of the active column flips ASC→DESC.
	if s.Asc() {
		t.Fatalf("Asc = true after flip, want false")
	}
	if !s.SelectByHotkey('N') {
		t.Fatalf("SelectByHotkey('N') second call = false")
	}
	if !s.Asc() {
		t.Fatalf("Asc = false after second flip, want true")
	}
}

func TestSelectByHotkeyResetsDirectionOnColumnSwitch(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	// Flip name to DESC, then switch to score: score must use its
	// default (DESC), not inherit name's transient direction.
	s.SelectByHotkey('N')
	if s.Asc() {
		t.Fatalf("setup: Asc still true after flip")
	}
	if !s.SelectByHotkey('C') {
		t.Fatalf("SelectByHotkey('C') = false, want true")
	}
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("ActiveKey = %q, want %q", got, keyScore)
	}
	if s.Asc() {
		t.Fatalf("Asc = true on switch to score, want false (its default)")
	}
}

func TestSelectByHotkeyUnknownReturnsFalse(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	if s.SelectByHotkey('Z') {
		t.Fatalf("SelectByHotkey('Z') = true, want false")
	}
	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("ActiveKey changed on unknown hotkey: %q", got)
	}
}

func TestWalkRightWraps(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	if !s.WalkRight() {
		t.Fatalf("WalkRight #1 = false")
	}
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("after WalkRight ActiveKey = %q, want %q", got, keyScore)
	}
	if !s.WalkRight() {
		t.Fatalf("WalkRight #2 = false")
	}
	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("WalkRight wrap ActiveKey = %q, want %q", got, keyName)
	}
}

func TestWalkLeftWraps(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	if !s.WalkLeft() {
		t.Fatalf("WalkLeft = false")
	}
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("WalkLeft wrap ActiveKey = %q, want %q", got, keyScore)
	}
}

func TestWalkResetsToColumnDefault(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	// Flip name to DESC; walking right to score should land on
	// score's DESC default, not inherit DESC from the previous flip.
	s.SelectByHotkey('N')
	s.WalkRight()
	if got := s.ActiveKey(); got != keyScore {
		t.Fatalf("ActiveKey = %q, want score", got)
	}
	if s.Asc() {
		t.Fatalf("Asc = true after walk, want score's DESC default")
	}
}

func TestWalkSingleColumnNoOp(t *testing.T) {
	t.Parallel()
	cols := []tablesort.Column[row]{
		{Key: keyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true, Less: nameLess},
	}
	s := tablesort.New(cols, keyName)
	if s.WalkRight() {
		t.Fatalf("WalkRight on 1-column = true, want false")
	}
	if s.WalkLeft() {
		t.Fatalf("WalkLeft on 1-column = true, want false")
	}
	if got := s.ActiveKey(); got != keyName {
		t.Fatalf("ActiveKey changed on single-col walk: %q", got)
	}
}

func TestArrowFor(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	if got := s.ArrowFor(keyName); got != "↑" {
		t.Fatalf("ArrowFor active+ASC = %q, want ↑", got)
	}
	if got := s.ArrowFor(keyScore); got != "" {
		t.Fatalf("ArrowFor inactive = %q, want empty", got)
	}
	s.SelectByHotkey('N') // flip to DESC
	if got := s.ArrowFor(keyName); got != "↓" {
		t.Fatalf("ArrowFor active+DESC = %q, want ↓", got)
	}
}

func TestIsActive(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	if !s.IsActive(keyName) {
		t.Fatalf("IsActive(name) = false, want true")
	}
	if s.IsActive(keyScore) {
		t.Fatalf("IsActive(score) = true, want false")
	}
}

func TestHandleKeyDispatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		key       string
		wantUsed  bool
		wantKey   string
		wantAscFn func(asc bool) bool // assertion on direction post-dispatch
	}{
		{
			name:      "uppercase letter form",
			key:       "C",
			wantUsed:  true,
			wantKey:   keyScore,
			wantAscFn: func(asc bool) bool { return !asc }, // score DESC default
		},
		{
			name:      "shift+letter long form",
			key:       "shift+c",
			wantUsed:  true,
			wantKey:   keyScore,
			wantAscFn: func(asc bool) bool { return !asc },
		},
		{
			name:      "h walks left",
			key:       "h",
			wantUsed:  true,
			wantKey:   keyScore, // wraps from name
			wantAscFn: func(asc bool) bool { return !asc },
		},
		{
			name:      "l walks right",
			key:       "l",
			wantUsed:  true,
			wantKey:   keyScore,
			wantAscFn: func(asc bool) bool { return !asc },
		},
		{
			name:      "left arrow walks left",
			key:       "left",
			wantUsed:  true,
			wantKey:   keyScore,
			wantAscFn: func(asc bool) bool { return !asc },
		},
		{
			name:      "right arrow walks right",
			key:       "right",
			wantUsed:  true,
			wantKey:   keyScore,
			wantAscFn: func(asc bool) bool { return !asc },
		},
		{
			name:      "unknown key not consumed",
			key:       "q",
			wantUsed:  false,
			wantKey:   keyName,
			wantAscFn: func(asc bool) bool { return asc },
		},
		{
			name:      "lowercase shift+letter form",
			key:       "shift+n",
			wantUsed:  true,
			wantKey:   keyName,                             // already active
			wantAscFn: func(asc bool) bool { return !asc }, // flips
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tablesort.New(fixtureCols(), keyName)
			got := s.HandleKey(tc.key)
			if got != tc.wantUsed {
				t.Fatalf("HandleKey(%q) = %v, want %v", tc.key, got, tc.wantUsed)
			}
			if k := s.ActiveKey(); k != tc.wantKey {
				t.Fatalf("after HandleKey(%q) ActiveKey = %q, want %q", tc.key, k, tc.wantKey)
			}
			if !tc.wantAscFn(s.Asc()) {
				t.Fatalf("after HandleKey(%q) direction unexpected (asc=%v)", tc.key, s.Asc())
			}
		})
	}
}

func TestBindingsEmitsShiftLetterPerColumn(t *testing.T) {
	t.Parallel()
	s := tablesort.New(fixtureCols(), keyName)
	got := s.Bindings("alerts")
	if len(got) != 2 {
		t.Fatalf("Bindings len = %d, want 2", len(got))
	}
	want := []action.Action{
		{Key: "Shift+N", Description: "sort by name", View: "alerts"},
		{Key: "Shift+C", Description: "sort by score", View: "alerts"},
	}
	for i, a := range got {
		if a != want[i] {
			t.Fatalf("Bindings[%d] = %+v, want %+v", i, a, want[i])
		}
	}
}

func TestBindingsSkipsZeroHotkeyColumns(t *testing.T) {
	t.Parallel()
	cols := []tablesort.Column[row]{
		{Key: keyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true, Less: nameLess},
		{Key: "hidden", Title: "HIDDEN", Hotkey: 0, DefaultAsc: true, Less: nameLess},
	}
	s := tablesort.New(cols, keyName)
	got := s.Bindings("alerts")
	if len(got) != 1 {
		t.Fatalf("Bindings len = %d, want 1 (zero-hotkey column omitted)", len(got))
	}
	if got[0].Key != "Shift+N" {
		t.Fatalf("Bindings[0].Key = %q, want Shift+N", got[0].Key)
	}
}

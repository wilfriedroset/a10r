// SPDX-License-Identifier: Apache-2.0

package tablesort_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
)

const resourceFixture = "fixture"

// fakeMemory is a tablesort.Memory stub recording the last value
// written, so a test can assert both the restore and the persist
// direction of the seam.
type fakeMemory struct {
	stored  map[string]string
	writes  int
	lastVal string
}

func newFakeMemory(seed string) *fakeMemory {
	m := &fakeMemory{stored: map[string]string{}}
	if seed != "" {
		m.stored[resourceFixture] = seed
	}
	return m
}

func (m *fakeMemory) Sort(resource string) string { return m.stored[resource] }

func (m *fakeMemory) SetSort(resource, value string) {
	m.writes++
	m.lastVal = value
	m.stored[resource] = value
}

func TestBind_RestoresRememberedColumn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		remembered string
		wantKey    string
		wantAsc    bool
	}{
		// The fixture's construction default is SCORE/DESC.
		{name: "unset leaves the default", remembered: "", wantKey: keyScore, wantAsc: false},
		{name: "restores column and asc", remembered: "name:asc", wantKey: keyName, wantAsc: true},
		{name: "restores column and desc", remembered: "name:desc", wantKey: keyName, wantAsc: false},
		// A DESC-by-default column remembered as ASC must come back
		// ASC, which is what proves Bind bypasses selectIndex's
		// same-column flip.
		{name: "restores non-default direction on the default column", remembered: "score:asc", wantKey: keyScore, wantAsc: true},
		{name: "column the page no longer has is ignored", remembered: "gone:asc", wantKey: keyScore, wantAsc: false},
		{name: "unparseable value is ignored", remembered: "name", wantKey: keyScore, wantAsc: false},
		{name: "unknown direction is ignored", remembered: "name:sideways", wantKey: keyScore, wantAsc: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mem := newFakeMemory(tc.remembered)
			s := tablesort.New(fixtureCols(), keyScore)
			s.Bind(mem, resourceFixture)

			require.Equal(t, tc.wantKey, s.ActiveKey())
			require.Equal(t, tc.wantAsc, s.Asc())
			require.Zero(t, mem.writes, "restoring must never write back")
		})
	}
}

func TestPersist_WritesEveryStateChange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		act  func(s *tablesort.Sorter[row])
		want string
	}{
		{name: "hotkey selects a column", act: func(s *tablesort.Sorter[row]) { s.SelectByHotkey('N') }, want: "name:asc"},
		{name: "key selects a column", act: func(s *tablesort.Sorter[row]) { s.SelectByKey(keyName) }, want: "name:asc"},
		{name: "repeat flips direction", act: func(s *tablesort.Sorter[row]) {
			s.SelectByHotkey('N')
			s.SelectByHotkey('N')
		}, want: "name:desc"},
		{name: "walk right", act: func(s *tablesort.Sorter[row]) { s.WalkRight() }, want: "name:asc"},
		{name: "walk left", act: func(s *tablesort.Sorter[row]) { s.WalkLeft() }, want: "name:asc"},
		// Back on the construction default: the entry is forgotten so
		// the state file stays small.
		{name: "reset forgets the entry", act: func(s *tablesort.Sorter[row]) {
			s.SelectByHotkey('N')
			s.Reset()
		}, want: ""},
		{name: "returning to the default by hotkey forgets the entry", act: func(s *tablesort.Sorter[row]) {
			s.SelectByHotkey('N')
			s.SelectByHotkey('C')
		}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mem := newFakeMemory("")
			s := tablesort.New(fixtureCols(), keyScore)
			s.Bind(mem, resourceFixture)

			tc.act(s)

			require.NotZero(t, mem.writes, "every state change must reach the memory")
			require.Equal(t, tc.want, mem.lastVal)
		})
	}
}

// TestUnboundSorterNeverPersists is the "sort memory off" contract:
// an unbound sorter behaves exactly as it did before the seam
// existed, including when Bind is handed a nil Memory.
func TestUnboundSorterNeverPersists(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		bindNil bool
	}{
		{name: "never bound"},
		{name: "bound to a nil memory", bindNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tablesort.New(fixtureCols(), keyScore)
			if tc.bindNil {
				s.Bind(nil, resourceFixture)
			}
			require.NotPanics(t, func() {
				s.SelectByHotkey('N')
				s.WalkRight()
				s.WalkLeft()
				s.Reset()
			})
			require.Equal(t, keyScore, s.ActiveKey())
		})
	}
}

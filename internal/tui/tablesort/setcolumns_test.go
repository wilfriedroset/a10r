// SPDX-License-Identifier: Apache-2.0

package tablesort_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
)

func TestSetColumns_KeepsAChoiceTheNewSetStillHas(t *testing.T) {
	t.Parallel()

	s := tablesort.New(fixtureCols(), keyScore)
	require.True(t, s.SelectByKey(keyName))
	require.True(t, s.SelectByKey(keyName), "a second press flips to DESC")

	s.SetColumns(append(fixtureCols(), tablesort.Column[row]{Key: "extra", Less: nameLess}))

	require.Equal(t, keyName, s.ActiveKey())
	require.False(t, s.Asc(), "the direction the user chose survives")
}

func TestSetColumns_FallsBackToTheDefaultWhenTheChoiceWentAway(t *testing.T) {
	t.Parallel()

	extra := tablesort.Column[row]{Key: "extra", DefaultAsc: true, Less: nameLess}
	s := tablesort.New(append(fixtureCols(), extra), keyScore)
	require.True(t, s.SelectByKey("extra"))

	s.SetColumns(fixtureCols())

	require.Equal(t, keyScore, s.ActiveKey())
	require.False(t, s.Asc(), "the default column's own direction, not the dropped one's")
	s.Reset()
	require.Equal(t, keyScore, s.ActiveKey(), "the default survives the swap")
}

// A reload must not unhook the sorter from its memory or its display
// tier: the first stops saving the operator's choice, the second lets
// a sort land on a column the operator cannot see.
func TestSetColumns_KeepsTheMemoryAndTheHiddenRule(t *testing.T) {
	t.Parallel()

	mem := newFakeMemory("")
	s := tablesort.New(tierCols(), keyName)
	s.Bind(mem, resourceFixture)
	s.SetHidden(func(key string) bool { return key == keyExtra })

	s.SetColumns(tierCols())

	require.True(t, s.SelectByKey(keyScore))
	require.Equal(t, 1, mem.writes, "a press after the swap still persists")
	require.False(t, s.SelectByKey(keyExtra), "a hidden column stays out of reach")
}

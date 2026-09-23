// SPDX-License-Identifier: Apache-2.0

package table_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/table"
)

func TestLabelColumn_ResolveMaterialisesTheConfiguredColumns(t *testing.T) {
	t.Parallel()

	got := table.Resolve([]config.Column{
		{Label: "cluster"},
		{Label: "pod", Title: "workload", SortKey: "P", Width: 9, Wide: true},
	})

	require.Equal(t, []table.LabelColumn{
		{Label: "cluster", Title: "CLUSTER", Key: "label:cluster", Index: 0},
		{Label: "pod", Title: "WORKLOAD", Key: "label:pod", Hotkey: 'P', Width: 9, Wide: true, Index: 1},
	}, got)
}

// A wide column is out of view until the operator presses Shift+W.
// The surviving columns keep their Index, which is what the cell
// slices and the sorter are keyed by, so hiding one cannot shift a
// row's cells under the remaining headers.
func TestLabelColumn_VisibleDropsWideColumnsUntilTheWideTier(t *testing.T) {
	t.Parallel()

	cols := table.Resolve([]config.Column{
		{Label: "cluster"},
		{Label: "pod", Wide: true},
		{Label: "team"},
	})

	narrow := table.Visible(cols, false)
	require.Equal(t, []string{"cluster", "team"}, labelsOf(narrow))
	require.Equal(t, []int{0, 2}, indicesOf(narrow))
	require.Equal(t, cols, table.Visible(cols, true))
}

// HasWide gates the Shift+W binding and its hint chip: a key that
// changes nothing must not be advertised.
func TestLabelColumn_HasWideGatesTheWideBinding(t *testing.T) {
	t.Parallel()

	plain := table.Resolve([]config.Column{{Label: "cluster"}})
	require.False(t, table.HasWide(plain))
	require.False(t, table.HasWide(nil))
	require.True(t, table.HasWide(table.Resolve([]config.Column{{Label: "pod", Wide: true}})))
}

func labelsOf(cols []table.LabelColumn) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Label)
	}
	return out
}

func indicesOf(cols []table.LabelColumn) []int {
	out := make([]int, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Index)
	}
	return out
}

func TestLabelColumn_ResolveEmptyStaysNil(t *testing.T) {
	t.Parallel()
	require.Nil(t, table.Resolve(nil))
}

// Markers rank after plain values, and the marker text itself is not
// special-cased by byte order: "<2 values>" starts with '<', which
// sorts before every letter in raw ASCII.
func TestCellLessRanksMarkersAfterPlainValuesThenByteWise(t *testing.T) {
	t.Parallel()

	in := []string{table.RollupMarker(2), "zebra", "alpha", table.RollupMarker(10)}
	sort.SliceStable(in, func(i, j int) bool { return table.CellLess(in[i], in[j]) })
	require.Equal(t, []string{"alpha", "zebra", "<10 values>", "<2 values>"}, in)
}

func TestCellEmptyPinsTheTail(t *testing.T) {
	t.Parallel()

	require.True(t, table.CellEmpty(""))
	require.False(t, table.CellEmpty("prod"))
	require.False(t, table.CellEmpty(table.RollupMarker(2)))
}

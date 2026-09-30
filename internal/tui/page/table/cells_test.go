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
func TestLabelSet_ShownDropsWideColumnsUntilTheWideTier(t *testing.T) {
	t.Parallel()

	s := table.NewLabelSet([]config.Column{
		{Label: "cluster"},
		{Label: "pod", Wide: true},
		{Label: "team"},
	})

	require.Equal(t, []string{"cluster", "team"}, labelsOf(s.Shown()))
	require.Equal(t, []int{0, 2}, indicesOf(s.Shown()))
	require.True(t, s.HiddenSortKey("label:pod"))
	require.False(t, s.HiddenSortKey("label:cluster"))
	require.False(t, s.HiddenSortKey("severity"))

	require.True(t, s.ToggleWide())
	require.Equal(t, s.All(), s.Shown())
	require.False(t, s.HiddenSortKey("label:pod"))
}

// HasWide gates the Shift+W binding and its hint chip: a key that
// changes nothing must not be advertised.
func TestLabelSet_HasWideGatesTheWideBinding(t *testing.T) {
	t.Parallel()

	plain := table.NewLabelSet([]config.Column{{Label: "cluster"}})
	require.False(t, plain.HasWide())
	require.False(t, plain.ToggleWide())
	empty := table.NewLabelSet(nil)
	require.False(t, empty.HasWide())
	wide := table.NewLabelSet([]config.Column{{Label: "pod", Wide: true}})
	require.True(t, wide.HasWide())
}

// A reload keeps the wide tier while a wide column survives it, and
// switches the tier off once none does. Otherwise a later reload that
// adds one back would open it already shown, with no Shift+W press.
func TestLabelSet_ReconfigureKeepsTheWideTierOnlyWithAWideColumn(t *testing.T) {
	t.Parallel()

	wide := config.Column{Label: "pod", Wide: true}
	s := table.NewLabelSet([]config.Column{wide})
	require.True(t, s.ToggleWide())

	s.Reconfigure([]config.Column{wide, {Label: "team"}})
	require.Equal(t, []string{"pod", "team"}, labelsOf(s.Shown()))

	s.Reconfigure(nil)
	s.Reconfigure([]config.Column{wide})
	require.Empty(t, s.Shown())
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

// Every row is measured, so a vertical scroll can never shift a
// width, and a pinned column is left at zero because LabelColumns
// never reads it.
func TestMeasureLabels(t *testing.T) {
	t.Parallel()

	rows := [][]string{
		{"prod", "web-a"},
		{"staging", "a-much-longer-pod-name"},
		{"dev"},
	}
	cell := func(r, i int) string {
		if i < len(rows[r]) {
			return rows[r][i]
		}
		return ""
	}
	tests := []struct {
		name string
		cols []table.LabelColumn
		rows int
		want []int
	}{
		{name: "no columns", cols: nil, rows: len(rows), want: nil},
		{
			name: "measured over every row",
			cols: []table.LabelColumn{{Index: 0}, {Index: 1}},
			rows: len(rows),
			want: []int{len("staging"), len("a-much-longer-pod-name")},
		},
		{
			name: "pinned width left at zero",
			cols: []table.LabelColumn{{Index: 0, Width: 3}, {Index: 1}},
			rows: len(rows),
			want: []int{0, len("a-much-longer-pod-name")},
		},
		{
			name: "read by config index",
			cols: []table.LabelColumn{{Index: 1}},
			rows: len(rows),
			want: []int{len("a-much-longer-pod-name")},
		},
		{name: "no rows", cols: []table.LabelColumn{{Index: 0}}, rows: 0, want: []int{0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, table.MeasureLabels(tt.cols, tt.rows, cell))
		})
	}
}

// A measured column flexes from a small floor rather than reserving
// its full width; a configured width pins both the floor and the
// content. The header floor is the layout pass's job, not this one's.
func TestLabelColumns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		col    table.LabelColumn
		widths []int
		want   table.Column
	}{
		{
			name:   "measured wide value flexes from the floor",
			col:    table.LabelColumn{Key: "label:pod", Title: "POD"},
			widths: []int{40},
			want:   table.Column{Key: "label:pod", Title: "POD", Min: 6, Content: 40, Weight: 1, Clip: table.ClipEllipsis},
		},
		{
			name:   "measured narrow value floors at its own width",
			col:    table.LabelColumn{Key: "label:env", Title: "ENV"},
			widths: []int{3},
			want:   table.Column{Key: "label:env", Title: "ENV", Min: 3, Content: 3, Weight: 1, Clip: table.ClipEllipsis},
		},
		{
			name: "missing width measures zero",
			col:  table.LabelColumn{Key: "label:env", Title: "ENV"},
			want: table.Column{Key: "label:env", Title: "ENV", Weight: 1, Clip: table.ClipEllipsis},
		},
		{
			name:   "configured width pins",
			col:    table.LabelColumn{Key: "label:pod", Title: "POD", Width: 9, Hotkey: 'P'},
			widths: []int{0},
			want:   table.Column{Key: "label:pod", Title: "POD", Sortable: true, Min: 9, Content: 9, Clip: table.ClipEllipsis},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, []table.Column{tt.want}, table.LabelColumns([]table.LabelColumn{tt.col}, tt.widths))
		})
	}
}

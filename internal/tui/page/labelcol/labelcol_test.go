// SPDX-License-Identifier: Apache-2.0

package labelcol_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/labelcol"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	got := labelcol.Resolve([]config.Column{
		{Label: "cluster"},
		{Label: "pod", Title: "workload", SortKey: "P", Width: 9, Wide: true},
	})

	require.Equal(t, []labelcol.Column{
		{Label: "cluster", Title: "CLUSTER", Key: "label:cluster", Index: 0},
		{Label: "pod", Title: "WORKLOAD", Key: "label:pod", Hotkey: 'P', Width: 9, Wide: true, Index: 1},
	}, got)
}

// A wide column is out of view until the operator presses Shift+W.
// The surviving columns keep their Index, which is what the cell
// slices and the sorter are keyed by, so hiding one cannot shift a
// row's cells under the remaining headers.
func TestVisibleDropsWideColumnsUntilTheWideTier(t *testing.T) {
	t.Parallel()

	cols := labelcol.Resolve([]config.Column{
		{Label: "cluster"},
		{Label: "pod", Wide: true},
		{Label: "team"},
	})

	narrow := labelcol.Visible(cols, false)
	require.Equal(t, []string{"cluster", "team"}, labelsOf(narrow))
	require.Equal(t, []int{0, 2}, indicesOf(narrow))
	require.Equal(t, cols, labelcol.Visible(cols, true))
}

// HasWide gates the Shift+W binding and its hint chip: a key that
// changes nothing must not be advertised.
func TestHasWide(t *testing.T) {
	t.Parallel()

	plain := labelcol.Resolve([]config.Column{{Label: "cluster"}})
	require.False(t, labelcol.HasWide(plain))
	require.False(t, labelcol.HasWide(nil))
	require.True(t, labelcol.HasWide(labelcol.Resolve([]config.Column{{Label: "pod", Wide: true}})))
}

func labelsOf(cols []labelcol.Column) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Label)
	}
	return out
}

func indicesOf(cols []labelcol.Column) []int {
	out := make([]int, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Index)
	}
	return out
}

func TestResolveEmptyStaysNil(t *testing.T) {
	t.Parallel()
	require.Nil(t, labelcol.Resolve(nil))
}

func TestAggregateCell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		instances []backend.Alert
		want      string
	}{
		{
			name: "no instances",
			want: "",
		},
		{
			name:      "every instance agrees",
			instances: alertsWith("prod", "prod", "prod"),
			want:      "prod",
		},
		{
			name:      "instances disagree",
			instances: alertsWith("prod", "staging"),
			want:      "<2 values>",
		},
		{
			name:      "no instance carries the label",
			instances: alertsWith("", "", ""),
			want:      "",
		},
		{
			name:      "a missing value is one distinct value",
			instances: alertsWith("prod", ""),
			want:      "<2 values>",
		},
		{
			name:      "distinct count ignores repeats",
			instances: alertsWith("prod", "staging", "prod", "dev"),
			want:      "<3 values>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, labelcol.AggregateCell(tt.instances, "cluster"))
		})
	}
}

// Markers rank after plain values, and the marker text itself is not
// special-cased by byte order: "<2 values>" starts with '<', which
// sorts before every letter in raw ASCII.
func TestLessRanksMarkersAfterPlainValuesThenByteWise(t *testing.T) {
	t.Parallel()

	in := []string{"<2 values>", "zebra", "alpha", "<10 values>"}
	sort.SliceStable(in, func(i, j int) bool { return labelcol.Less(in[i], in[j]) })
	require.Equal(t, []string{"alpha", "zebra", "<10 values>", "<2 values>"}, in)
}

func TestIsEmptyPinsTheTail(t *testing.T) {
	t.Parallel()

	require.True(t, labelcol.IsEmpty(""))
	require.False(t, labelcol.IsEmpty("prod"))
	require.False(t, labelcol.IsEmpty("<2 values>"))
}

// The arrow reserve is conditional: a column with no sort_key never
// renders one, so floors at its plain title.
func TestFloorNeverGoesUnderTheHeader(t *testing.T) {
	t.Parallel()

	sortable := labelcol.Column{Title: "CLUSTER", Hotkey: 'L'}
	plain := labelcol.Column{Title: "CLUSTER"}

	require.Equal(t, 9, labelcol.Floor(sortable, 0))
	require.Equal(t, 9, labelcol.Floor(sortable, 3))
	require.Equal(t, 20, labelcol.Floor(sortable, 20))
	require.Equal(t, 7, labelcol.Floor(plain, 3))
	require.Equal(t, 20, labelcol.Floor(plain, 20))
}

func alertsWith(values ...string) []backend.Alert {
	out := make([]backend.Alert, 0, len(values))
	for _, v := range values {
		labels := map[string]string{"alertname": "DiskFull"}
		if v != "" {
			labels["cluster"] = v
		}
		out = append(out, backend.Alert{Labels: labels})
	}
	return out
}

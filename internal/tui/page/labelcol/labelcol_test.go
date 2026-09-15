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
		{Label: "cluster", Title: "CLUSTER", Key: "label:cluster"},
		{Label: "pod", Title: "WORKLOAD", Key: "label:pod", Hotkey: 'P', Width: 9, Wide: true},
	}, got)
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

// SPDX-License-Identifier: Apache-2.0

package cursor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

// fakeSorter is a tiny sort handler whose HandleKey returns the
// configured value and records the key it received.
type fakeSorter struct {
	consume bool
	gotKey  string
}

func (f *fakeSorter) HandleKey(key string) bool {
	f.gotKey = key
	return f.consume
}

func TestHandleSort_KeyConsumed(t *testing.T) {
	t.Parallel()

	sorter := &fakeSorter{consume: true}
	// callOrder pins the contract that clearFocus runs before
	// recompute — the find-by-key bypass only works in that order.
	var callOrder []string

	got := cursor.HandleSort(
		"L",
		sorter,
		func() { callOrder = append(callOrder, "clear") },
		func() { callOrder = append(callOrder, "recompute") },
	)

	require.True(t, got, "consumed sort key must report handled=true")
	require.Equal(t, "L", sorter.gotKey)
	require.Equal(t, []string{"clear", "recompute"}, callOrder,
		"clearFocus must run before recompute")
}

func TestHandleSort_KeyIgnored(t *testing.T) {
	t.Parallel()

	sorter := &fakeSorter{consume: false}
	cleared, recomputed := false, false

	got := cursor.HandleSort(
		"x",
		sorter,
		func() { cleared = true },
		func() { recomputed = true },
	)

	require.False(t, got, "non-sort key must report handled=false")
	require.Equal(t, "x", sorter.gotKey, "sorter must still be probed")
	require.False(t, cleared, "clearFocus must NOT run on an ignored key")
	require.False(t, recomputed, "recompute must NOT run on an ignored key")
}

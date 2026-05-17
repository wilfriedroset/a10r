// SPDX-License-Identifier: Apache-2.0

package cursor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

func TestClamp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		cursor    int
		itemCount int
		want      int
	}{
		// empty list collapses to 0
		{name: "empty list with cursor 0 returns 0", cursor: 0, itemCount: 0, want: 0},
		{name: "empty list with positive cursor returns 0", cursor: 5, itemCount: 0, want: 0},
		{name: "empty list with negative cursor returns 0", cursor: -3, itemCount: 0, want: 0},
		{name: "negative itemCount returns 0", cursor: 2, itemCount: -1, want: 0},

		// negative cursor clamps to 0
		{name: "negative cursor with populated list returns 0", cursor: -1, itemCount: 10, want: 0},
		{name: "very negative cursor returns 0", cursor: -999, itemCount: 10, want: 0},

		// cursor inside range is returned unchanged
		{name: "cursor at 0 in populated list", cursor: 0, itemCount: 10, want: 0},
		{name: "cursor in middle of range", cursor: 4, itemCount: 10, want: 4},
		{name: "cursor at last valid index", cursor: 9, itemCount: 10, want: 9},

		// cursor beyond last valid index clamps down
		{name: "cursor one past end clamps to last", cursor: 10, itemCount: 10, want: 9},
		{name: "cursor far past end clamps to last", cursor: 999, itemCount: 10, want: 9},

		// single-element list
		{name: "single item with cursor 0", cursor: 0, itemCount: 1, want: 0},
		{name: "single item with cursor past end", cursor: 5, itemCount: 1, want: 0},

		// large itemCount
		{name: "large list, cursor in range", cursor: 12345, itemCount: 1_000_000, want: 12345},
		{name: "large list, cursor at last index", cursor: 999_999, itemCount: 1_000_000, want: 999_999},
		{name: "large list, cursor past end", cursor: 1_000_000, itemCount: 1_000_000, want: 999_999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cursor.Clamp(tc.cursor, tc.itemCount)
			require.Equal(t, tc.want, got)

			// Range invariant: result is always 0 for empty lists, or
			// inside [0, itemCount-1] otherwise. Catches future refactors
			// that pass the table but break the contract.
			if tc.itemCount <= 0 {
				require.Equal(t, 0, got)
			} else {
				require.GreaterOrEqual(t, got, 0)
				require.Less(t, got, tc.itemCount)
			}
		})
	}
}

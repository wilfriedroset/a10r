// SPDX-License-Identifier: Apache-2.0

package cursor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

func TestReconcileScroll(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		cursor    int
		topRow    int
		maxRows   int
		totalRows int
		want      int
	}{
		// happy paths
		{name: "cursor inside window: topRow unchanged", cursor: 5, topRow: 0, maxRows: 10, totalRows: 100, want: 0},
		{name: "cursor at top: topRow unchanged", cursor: 0, topRow: 0, maxRows: 10, totalRows: 100, want: 0},
		{name: "cursor at last visible row: topRow unchanged", cursor: 9, topRow: 0, maxRows: 10, totalRows: 100, want: 0},

		// cursor above window snaps top to cursor
		{name: "cursor above window snaps to cursor", cursor: 3, topRow: 10, maxRows: 10, totalRows: 100, want: 3},
		{name: "cursor at zero with high topRow", cursor: 0, topRow: 50, maxRows: 10, totalRows: 100, want: 0},

		// cursor below window advances top
		{name: "cursor one past window advances top by one", cursor: 10, topRow: 0, maxRows: 10, totalRows: 100, want: 1},
		{name: "cursor far below window keeps it on last visible row", cursor: 50, topRow: 0, maxRows: 10, totalRows: 100, want: 41},

		// max clamp: never scroll past the last possible window
		{name: "topRow exceeds maxTop is clamped", cursor: 99, topRow: 999, maxRows: 10, totalRows: 100, want: 90},
		{name: "cursor at last row aligns window to end", cursor: 99, topRow: 0, maxRows: 10, totalRows: 100, want: 90},

		// negative clamp
		{name: "negative topRow with cursor 0 clamps to 0", cursor: 0, topRow: -5, maxRows: 10, totalRows: 100, want: 0},

		// empty list / zero rows
		{name: "totalRows zero clamps to 0", cursor: 0, topRow: 0, maxRows: 10, totalRows: 0, want: 0},
		{name: "totalRows zero with stale cursor clamps to 0", cursor: 5, topRow: 5, maxRows: 10, totalRows: 0, want: 0},

		// view smaller than window
		{name: "totalRows fits in window, top stays 0", cursor: 2, topRow: 0, maxRows: 10, totalRows: 5, want: 0},
		{name: "totalRows fits in window, stale top clamps", cursor: 2, topRow: 7, maxRows: 10, totalRows: 5, want: 0},

		// degenerate maxRows: documented behaviour, not a panic.
		// Callers always pass maxRows >= 1 in production; these
		// rows pin "doesn't panic, returns a deterministic int".
		{name: "maxRows zero with cursor in range advances top to cursor+1", cursor: 5, topRow: 0, maxRows: 0, totalRows: 100, want: 6},
		{name: "negative maxRows with cursor 0 advances by maxRows-1", cursor: 0, topRow: 0, maxRows: -3, totalRows: 100, want: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cursor.ReconcileScroll(tc.cursor, tc.topRow, tc.maxRows, tc.totalRows)
			require.Equal(t, tc.want, got)

			// Visibility invariant: when maxRows > 0 and cursor is
			// inside the row list, the cursor must land inside the
			// returned window. Future refactors that pass the table
			// but break this contract get caught here.
			if tc.maxRows > 0 && tc.cursor >= 0 && tc.cursor < tc.totalRows {
				require.GreaterOrEqual(t, tc.cursor, got,
					"cursor must be >= topRow")
				require.Less(t, tc.cursor, got+tc.maxRows,
					"cursor must be < topRow+maxRows")
			}
		})
	}
}

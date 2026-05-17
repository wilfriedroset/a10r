// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_ClampCursor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		cursor    int
		itemCount int
		want      int
	}{
		{name: "cursor in range unchanged", cursor: 3, itemCount: 10, want: 3},
		{name: "cursor past end clamps to last", cursor: 12, itemCount: 5, want: 4},
		{name: "cursor at boundary clamps down", cursor: 5, itemCount: 5, want: 4},
		{name: "empty list collapses cursor to 0", cursor: 4, itemCount: 0, want: 0},
		{name: "negative cursor clamps to 0", cursor: -2, itemCount: 5, want: 0},
		{name: "cursor 0 in populated list stays", cursor: 0, itemCount: 5, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{Cursor: tc.cursor}
			b.ClampCursor(tc.itemCount)
			require.Equal(t, tc.want, b.Cursor)
		})
	}
}

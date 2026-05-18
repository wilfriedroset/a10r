// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_ReconcileScroll(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cursor     int
		topRow     int
		bodyHeight int
		itemCount  int
		wantTop    int
	}{
		{name: "zero body height is no-op", cursor: 5, topRow: 2, bodyHeight: 0, itemCount: 10, wantTop: 2},
		{name: "cursor inside window keeps top", cursor: 3, topRow: 0, bodyHeight: 5, itemCount: 10, wantTop: 0},
		{name: "cursor above window snaps top to cursor", cursor: 1, topRow: 4, bodyHeight: 5, itemCount: 10, wantTop: 1},
		{name: "cursor below window advances top", cursor: 9, topRow: 0, bodyHeight: 5, itemCount: 10, wantTop: 5},
		{name: "top is clamped to max scrollable", cursor: 9, topRow: 8, bodyHeight: 5, itemCount: 10, wantTop: 5},
		{name: "empty list keeps top at 0", cursor: 0, topRow: 0, bodyHeight: 5, itemCount: 0, wantTop: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Cursor:     tc.cursor,
				TopRow:     tc.topRow,
				BodyHeight: tc.bodyHeight,
			}
			b.ReconcileScroll(tc.itemCount)
			require.Equal(t, tc.wantTop, b.TopRow)
		})
	}
}

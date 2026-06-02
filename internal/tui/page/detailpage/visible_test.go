// SPDX-License-Identifier: Apache-2.0

package detailpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
)

func TestBase_Visible(t *testing.T) {
	t.Parallel()

	lines10 := []string{"l0", "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}

	cases := []struct {
		name           string
		initialScroll  int
		lines          []string
		height         int
		wantVisible    []string
		wantScroll     int
		wantBodyHeight int
	}{
		{
			name:           "empty body",
			initialScroll:  3,
			lines:          nil,
			height:         5,
			wantVisible:    nil,
			wantScroll:     0,
			wantBodyHeight: 5,
		},
		{
			name:           "height bigger than body shows everything from top",
			initialScroll:  7,
			lines:          lines10,
			height:         20,
			wantVisible:    lines10,
			wantScroll:     0,
			wantBodyHeight: 20,
		},
		{
			name:           "scroll in middle returns the window",
			initialScroll:  2,
			lines:          lines10,
			height:         4,
			wantVisible:    []string{"l2", "l3", "l4", "l5"},
			wantScroll:     2,
			wantBodyHeight: 4,
		},
		{
			name:           "scroll past end clamps to maxScroll",
			initialScroll:  100,
			lines:          lines10,
			height:         4,
			wantVisible:    []string{"l6", "l7", "l8", "l9"},
			wantScroll:     6,
			wantBodyHeight: 4,
		},
		{
			name:           "G sentinel clamps to maxScroll",
			initialScroll:  1 << 30,
			lines:          lines10,
			height:         3,
			wantVisible:    []string{"l7", "l8", "l9"},
			wantScroll:     7,
			wantBodyHeight: 3,
		},
		{
			name:           "scroll at maxScroll returns final window",
			initialScroll:  6,
			lines:          lines10,
			height:         4,
			wantVisible:    []string{"l6", "l7", "l8", "l9"},
			wantScroll:     6,
			wantBodyHeight: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &detailpage.Base{Scroll: tc.initialScroll}
			got := b.Visible(tc.lines, tc.height)
			require.Equal(t, tc.wantVisible, got)
			require.Equal(t, tc.wantScroll, b.Scroll, "Scroll mismatch after Visible")
			require.Equal(t, tc.wantBodyHeight, b.BodyHeight, "BodyHeight mismatch after Visible")
		})
	}
}

// SPDX-License-Identifier: Apache-2.0

package cursor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

func TestHalfPageStep(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		bodyHeight int
		want       int
	}{
		{name: "unsized falls back to floor", bodyHeight: 0, want: 10},
		{name: "below threshold uses floor", bodyHeight: 1, want: 10},
		{name: "boundary halves down to 1", bodyHeight: 2, want: 1},
		{name: "three halves to 1", bodyHeight: 3, want: 1},
		{name: "twenty four halves to twelve", bodyHeight: 24, want: 12},
		{name: "fifty halves to twenty five", bodyHeight: 50, want: 25},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, cursor.HalfPageStep(tc.bodyHeight))
		})
	}
}

func TestFullPageStep(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		bodyHeight int
		want       int
	}{
		{name: "unsized falls back to floor", bodyHeight: 0, want: 20},
		{name: "below threshold uses floor", bodyHeight: 3, want: 20},
		{name: "boundary returns body minus two", bodyHeight: 4, want: 2},
		{name: "five returns three", bodyHeight: 5, want: 3},
		{name: "twenty four returns twenty two", bodyHeight: 24, want: 22},
		{name: "fifty returns forty eight", bodyHeight: 50, want: 48},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, cursor.FullPageStep(tc.bodyHeight))
		})
	}
}

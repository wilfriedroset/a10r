// SPDX-License-Identifier: Apache-2.0

package cursor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

func TestHandleMotion(t *testing.T) {
	t.Parallel()

	const halfStep, fullStep = 5, 12

	cases := []struct {
		name     string
		key      string
		cursor   int
		rowCount int
		want     int
		handled  bool
	}{
		// j / down
		{name: "j advances", key: "j", cursor: 3, rowCount: 10, want: 4, handled: true},
		{name: "down advances", key: "down", cursor: 3, rowCount: 10, want: 4, handled: true},
		{name: "j at last clamps", key: "j", cursor: 9, rowCount: 10, want: 9, handled: true},
		{name: "j on empty stays at 0", key: "j", cursor: 0, rowCount: 0, want: 0, handled: true},

		// k / up
		{name: "k retreats", key: "k", cursor: 3, rowCount: 10, want: 2, handled: true},
		{name: "up retreats", key: "up", cursor: 3, rowCount: 10, want: 2, handled: true},
		{name: "k at first clamps", key: "k", cursor: 0, rowCount: 10, want: 0, handled: true},

		// G
		{name: "G jumps to last", key: "G", cursor: 0, rowCount: 10, want: 9, handled: true},
		{name: "G on empty stays at 0", key: "G", cursor: 0, rowCount: 0, want: 0, handled: true},

		// ctrl+d / ctrl+u (half step)
		{name: "ctrl+d advances by half", key: "ctrl+d", cursor: 0, rowCount: 100, want: 5, handled: true},
		{name: "ctrl+d clamps to last", key: "ctrl+d", cursor: 8, rowCount: 10, want: 9, handled: true},
		{name: "ctrl+u retreats by half", key: "ctrl+u", cursor: 8, rowCount: 100, want: 3, handled: true},
		{name: "ctrl+u clamps to 0", key: "ctrl+u", cursor: 2, rowCount: 100, want: 0, handled: true},

		// ctrl+f / ctrl+b (full step)
		{name: "ctrl+f advances by full", key: "ctrl+f", cursor: 0, rowCount: 100, want: 12, handled: true},
		{name: "ctrl+f clamps to last", key: "ctrl+f", cursor: 95, rowCount: 100, want: 99, handled: true},
		{name: "ctrl+b retreats by full", key: "ctrl+b", cursor: 50, rowCount: 100, want: 38, handled: true},
		{name: "ctrl+b clamps to 0", key: "ctrl+b", cursor: 5, rowCount: 100, want: 0, handled: true},

		// unhandled
		{name: "unknown returns cursor unchanged", key: "x", cursor: 7, rowCount: 10, want: 7, handled: false},
		{name: "empty key not handled", key: "", cursor: 7, rowCount: 10, want: 7, handled: false},

		// rowCount==1: every motion returns cursor=0, no underflow
		{name: "j on single-row stays at 0", key: "j", cursor: 0, rowCount: 1, want: 0, handled: true},
		{name: "G on single-row stays at 0", key: "G", cursor: 0, rowCount: 1, want: 0, handled: true},
		{name: "ctrl+d on single-row stays at 0", key: "ctrl+d", cursor: 0, rowCount: 1, want: 0, handled: true},
		{name: "ctrl+u on single-row stays at 0", key: "ctrl+u", cursor: 0, rowCount: 1, want: 0, handled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, handled := cursor.HandleMotion(tc.key, tc.cursor, tc.rowCount, halfStep, fullStep)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.handled, handled)
		})
	}
}

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

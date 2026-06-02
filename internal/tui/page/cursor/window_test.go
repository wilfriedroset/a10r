// SPDX-License-Identifier: Apache-2.0

package cursor_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

func TestWindow_ZeroValueIsUsable(t *testing.T) {
	t.Parallel()

	var w cursor.Window
	require.Equal(t, 0, w.Index())
	require.Equal(t, 0, w.TopRow())
}

func TestNewWindow(t *testing.T) {
	t.Parallel()

	w := cursor.NewWindow(3, 1, 5)
	require.Equal(t, 3, w.Index())
	require.Equal(t, 1, w.TopRow())
}

func TestWindow_MoveCursor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		cursor      int
		topRow      int
		bodyHeight  int
		items       int
		key         string
		wantIndex   int
		wantTop     int
		wantChanged bool
		wantHandled bool
	}{
		// j / down basics
		{name: "j advances", cursor: 3, topRow: 0, bodyHeight: 10, items: 20, key: "j", wantIndex: 4, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "down advances", cursor: 3, topRow: 0, bodyHeight: 10, items: 20, key: "down", wantIndex: 4, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "k retreats", cursor: 3, topRow: 0, bodyHeight: 10, items: 20, key: "k", wantIndex: 2, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "up retreats", cursor: 3, topRow: 0, bodyHeight: 10, items: 20, key: "up", wantIndex: 2, wantTop: 0, wantChanged: true, wantHandled: true},

		// j at last row: handled but not changed (no focus snapshot)
		{name: "j at last is handled but not changed", cursor: 9, topRow: 0, bodyHeight: 10, items: 10, key: "j", wantIndex: 9, wantTop: 0, wantChanged: false, wantHandled: true},
		{name: "k at row 0 is handled but not changed", cursor: 0, topRow: 0, bodyHeight: 10, items: 10, key: "k", wantIndex: 0, wantTop: 0, wantChanged: false, wantHandled: true},

		// j before first WindowSizeMsg (bodyHeight==0) still moves by 1
		{name: "j before sizing moves by 1", cursor: 0, topRow: 0, bodyHeight: 0, items: 50, key: "j", wantIndex: 1, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "k before sizing moves by 1", cursor: 5, topRow: 0, bodyHeight: 0, items: 50, key: "k", wantIndex: 4, wantTop: 0, wantChanged: true, wantHandled: true},

		// G jumps to last
		{name: "G jumps to last", cursor: 0, topRow: 0, bodyHeight: 10, items: 100, key: "G", wantIndex: 99, wantTop: 90, wantChanged: true, wantHandled: true},
		{name: "G on empty is handled, no change", cursor: 0, topRow: 0, bodyHeight: 10, items: 0, key: "G", wantIndex: 0, wantTop: 0, wantChanged: false, wantHandled: true},
		{name: "G when already on last is handled but unchanged", cursor: 9, topRow: 0, bodyHeight: 10, items: 10, key: "G", wantIndex: 9, wantTop: 0, wantChanged: false, wantHandled: true},

		// Ctrl+D/U use bodyHeight/2 with floor of 10 when unsized
		{name: "ctrl+d at top advances by half", cursor: 0, topRow: 0, bodyHeight: 20, items: 100, key: "ctrl+d", wantIndex: 10, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "ctrl+d unsized advances by 10 floor", cursor: 0, topRow: 0, bodyHeight: 0, items: 100, key: "ctrl+d", wantIndex: 10, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "ctrl+u retreats by half", cursor: 12, topRow: 5, bodyHeight: 20, items: 100, key: "ctrl+u", wantIndex: 2, wantTop: 2, wantChanged: true, wantHandled: true},
		{name: "ctrl+u clamps to 0", cursor: 2, topRow: 0, bodyHeight: 20, items: 100, key: "ctrl+u", wantIndex: 0, wantTop: 0, wantChanged: true, wantHandled: true},

		// Ctrl+F/B use bodyHeight-2 with floor of 20 when unsized
		{name: "ctrl+f advances by body-2", cursor: 0, topRow: 0, bodyHeight: 20, items: 100, key: "ctrl+f", wantIndex: 18, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "ctrl+f unsized advances by 20 floor, leaves topRow untouched", cursor: 0, topRow: 0, bodyHeight: 0, items: 100, key: "ctrl+f", wantIndex: 20, wantTop: 0, wantChanged: true, wantHandled: true},
		{name: "ctrl+b retreats by body-2", cursor: 50, topRow: 32, bodyHeight: 20, items: 100, key: "ctrl+b", wantIndex: 32, wantTop: 32, wantChanged: true, wantHandled: true},

		// Reconcile: cursor below window advances top
		{name: "j past visible window advances top", cursor: 9, topRow: 0, bodyHeight: 10, items: 100, key: "j", wantIndex: 10, wantTop: 1, wantChanged: true, wantHandled: true},
		// Reconcile: cursor above window snaps top
		{name: "k above window snaps top", cursor: 10, topRow: 10, bodyHeight: 10, items: 100, key: "k", wantIndex: 9, wantTop: 9, wantChanged: true, wantHandled: true},

		// Empty list never panics
		{name: "j on empty list", cursor: 0, topRow: 0, bodyHeight: 10, items: 0, key: "j", wantIndex: 0, wantTop: 0, wantChanged: false, wantHandled: true},
		{name: "k on empty list", cursor: 0, topRow: 0, bodyHeight: 10, items: 0, key: "k", wantIndex: 0, wantTop: 0, wantChanged: false, wantHandled: true},
		{name: "ctrl+d on empty list", cursor: 0, topRow: 0, bodyHeight: 10, items: 0, key: "ctrl+d", wantIndex: 0, wantTop: 0, wantChanged: false, wantHandled: true},

		// Unhandled keys
		{name: "unknown key not handled, no change", cursor: 7, topRow: 0, bodyHeight: 10, items: 50, key: "x", wantIndex: 7, wantTop: 0, wantChanged: false, wantHandled: false},
		{name: "empty key not handled, no change", cursor: 7, topRow: 0, bodyHeight: 10, items: 50, key: "", wantIndex: 7, wantTop: 0, wantChanged: false, wantHandled: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := cursor.NewWindow(tc.cursor, tc.topRow, tc.bodyHeight)
			changed, handled := w.MoveCursor(tc.key, tc.items)
			require.Equal(t, tc.wantChanged, changed, "changed")
			require.Equal(t, tc.wantHandled, handled, "handled")
			require.Equal(t, tc.wantIndex, w.Index(), "index")
			require.Equal(t, tc.wantTop, w.TopRow(), "topRow")
		})
	}
}

func TestWindow_SetIndex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cursor     int
		topRow     int
		bodyHeight int
		items      int
		setTo      int
		wantIndex  int
		wantTop    int
	}{
		{name: "in range, reconciles top", cursor: 0, topRow: 0, bodyHeight: 10, items: 100, setTo: 50, wantIndex: 50, wantTop: 41},
		{name: "above current window snaps top", cursor: 50, topRow: 41, bodyHeight: 10, items: 100, setTo: 5, wantIndex: 5, wantTop: 5},
		{name: "clamps past end", cursor: 0, topRow: 0, bodyHeight: 10, items: 5, setTo: 99, wantIndex: 4, wantTop: 0},
		{name: "clamps below zero", cursor: 5, topRow: 0, bodyHeight: 10, items: 20, setTo: -3, wantIndex: 0, wantTop: 0},
		{name: "empty list clamps to 0", cursor: 0, topRow: 0, bodyHeight: 10, items: 0, setTo: 7, wantIndex: 0, wantTop: 0},
		{name: "unsized still updates index", cursor: 0, topRow: 0, bodyHeight: 0, items: 100, setTo: 50, wantIndex: 50, wantTop: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := cursor.NewWindow(tc.cursor, tc.topRow, tc.bodyHeight)
			w.SetIndex(tc.setTo, tc.items)
			require.Equal(t, tc.wantIndex, w.Index())
			require.Equal(t, tc.wantTop, w.TopRow())
		})
	}
}

func TestWindow_SetViewport(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cursor     int
		topRow     int
		bodyHeight int
		items      int
		newHeight  int
		wantIndex  int
		wantTop    int
	}{
		{name: "first sizing reconciles", cursor: 50, topRow: 0, bodyHeight: 0, items: 100, newHeight: 10, wantIndex: 50, wantTop: 41},
		{name: "resize smaller pushes top up", cursor: 30, topRow: 25, bodyHeight: 20, items: 100, newHeight: 5, wantIndex: 30, wantTop: 26},
		{name: "resize larger keeps cursor visible", cursor: 5, topRow: 5, bodyHeight: 5, items: 100, newHeight: 20, wantIndex: 5, wantTop: 5},
		{name: "empty list stays at 0", cursor: 0, topRow: 0, bodyHeight: 10, items: 0, newHeight: 20, wantIndex: 0, wantTop: 0},
		{name: "zero new height does not crash", cursor: 5, topRow: 2, bodyHeight: 10, items: 20, newHeight: 0, wantIndex: 5, wantTop: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := cursor.NewWindow(tc.cursor, tc.topRow, tc.bodyHeight)
			w.SetViewport(tc.newHeight, tc.items)
			require.Equal(t, tc.wantIndex, w.Index())
			require.Equal(t, tc.wantTop, w.TopRow())
		})
	}
}

func TestWindow_Clamp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		cursor     int
		topRow     int
		bodyHeight int
		items      int
		wantIndex  int
		wantTop    int
	}{
		{name: "cursor past end clamps", cursor: 50, topRow: 0, bodyHeight: 10, items: 20, wantIndex: 19, wantTop: 10},
		{name: "empty list resets both", cursor: 5, topRow: 3, bodyHeight: 10, items: 0, wantIndex: 0, wantTop: 0},
		{name: "negative cursor clamps to 0", cursor: -3, topRow: 0, bodyHeight: 10, items: 20, wantIndex: 0, wantTop: 0},
		{name: "in-range unchanged but top reconciles", cursor: 5, topRow: 0, bodyHeight: 10, items: 20, wantIndex: 5, wantTop: 0},
		{name: "stale topRow clamped after items shrink", cursor: 2, topRow: 50, bodyHeight: 10, items: 5, wantIndex: 2, wantTop: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := cursor.NewWindow(tc.cursor, tc.topRow, tc.bodyHeight)
			w.Clamp(tc.items)
			require.Equal(t, tc.wantIndex, w.Index())
			require.Equal(t, tc.wantTop, w.TopRow())
		})
	}
}

// Window resize while cursor is below new bottom: cursor stays put,
// topRow advances so cursor sits on last visible row.
func TestWindow_ResizeKeepsCursorVisible(t *testing.T) {
	t.Parallel()

	w := cursor.NewWindow(80, 0, 100)
	w.SetViewport(10, 100)
	require.Equal(t, 80, w.Index())
	require.Equal(t, 71, w.TopRow())
	require.GreaterOrEqual(t, w.Index(), w.TopRow())
	require.Less(t, w.Index(), w.TopRow()+10)
}

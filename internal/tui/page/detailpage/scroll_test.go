// SPDX-License-Identifier: Apache-2.0

package detailpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
)

func TestBase_HandleScrollKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		key         string
		initial     int
		bodyHeight  int
		wantHandled bool
		wantScroll  int
	}{
		{
			name:        "j increments by 1",
			key:         "j",
			initial:     3,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  4,
		},
		{
			name:        "down increments by 1",
			key:         "down",
			initial:     0,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  1,
		},
		{
			name:        "k decrements by 1 above 0",
			key:         "k",
			initial:     5,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  4,
		},
		{
			name:        "k floors at 0",
			key:         "k",
			initial:     0,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  0,
		},
		{
			name:        "up floors at 0",
			key:         "up",
			initial:     0,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  0,
		},
		{
			name:        "G pins past end so renderer clamps",
			key:         "G",
			initial:     0,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  1 << 30,
		},
		{
			name:        "ctrl+d steps half-page",
			key:         "ctrl+d",
			initial:     0,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  10,
		},
		{
			name:        "ctrl+u floors at 0 after half-page step",
			key:         "ctrl+u",
			initial:     3,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  0,
		},
		{
			name:        "ctrl+f steps full-page",
			key:         "ctrl+f",
			initial:     0,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  18,
		},
		{
			name:        "ctrl+b floors at 0 after full-page step",
			key:         "ctrl+b",
			initial:     3,
			bodyHeight:  20,
			wantHandled: true,
			wantScroll:  0,
		},
		{
			name:        "ctrl+d falls back to 10 when bodyHeight is zero",
			key:         "ctrl+d",
			initial:     0,
			bodyHeight:  0,
			wantHandled: true,
			wantScroll:  10,
		},
		{
			name:        "ctrl+f falls back to 20 when bodyHeight is zero",
			key:         "ctrl+f",
			initial:     0,
			bodyHeight:  0,
			wantHandled: true,
			wantScroll:  20,
		},
		{
			name:        "unknown key falls through",
			key:         "q",
			initial:     7,
			bodyHeight:  20,
			wantHandled: false,
			wantScroll:  7,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &detailpage.Base{Scroll: tc.initial, BodyHeight: tc.bodyHeight}
			got := b.HandleScrollKey(tc.key)
			require.Equal(t, tc.wantHandled, got, "handled mismatch")
			require.Equal(t, tc.wantScroll, b.Scroll, "Scroll mismatch")
		})
	}
}

func TestBase_ReconcileScroll(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		scroll     int
		totalLines int
		height     int
		want       int
	}{
		{name: "negative floors to 0", scroll: -5, totalLines: 50, height: 10, want: 0},
		{name: "within bounds untouched", scroll: 5, totalLines: 50, height: 10, want: 5},
		{name: "past end clamps to maxScroll", scroll: 100, totalLines: 50, height: 10, want: 40},
		{name: "G sentinel clamps to maxScroll", scroll: 1 << 30, totalLines: 50, height: 10, want: 40},
		{name: "body fits in height — maxScroll 0", scroll: 8, totalLines: 5, height: 10, want: 0},
		{name: "empty body — maxScroll 0", scroll: 5, totalLines: 0, height: 10, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &detailpage.Base{Scroll: tc.scroll}
			b.ReconcileScroll(tc.totalLines, tc.height)
			require.Equal(t, tc.want, b.Scroll)
		})
	}
}

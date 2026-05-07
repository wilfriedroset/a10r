// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

func TestPadRight(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    string
		w    int
		want string
	}{
		{name: "pads short string with spaces", s: "ab", w: 5, want: "ab   "},
		{name: "exact width returns as-is", s: "abcd", w: 4, want: "abcd"},
		{name: "longer string truncates to w", s: "abcdef", w: 4, want: "abcd"},
		{name: "empty string pads to w", s: "", w: 3, want: "   "},
		{name: "zero width returns empty", s: "abc", w: 0, want: ""},
		{name: "negative width returns empty", s: "abc", w: -1, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := format.PadRight(tc.s, tc.w)
			require.Equal(t, tc.want, got)
			if tc.w > 0 {
				require.Equal(t, tc.w, lipgloss.Width(got),
					"PadRight must produce a string of exactly w cells when w > 0")
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    string
		w    int
		want string
	}{
		{name: "shorter than w returns as-is", s: "abc", w: 5, want: "abc"},
		{name: "exact width returns as-is", s: "abcd", w: 4, want: "abcd"},
		{name: "longer truncates", s: "abcdef", w: 3, want: "abc"},
		{name: "zero width returns empty", s: "abc", w: 0, want: ""},
		{name: "negative width returns empty", s: "abc", w: -1, want: ""},
		{name: "empty string returns empty", s: "", w: 5, want: ""},
		// Width-aware: CJK chars count as 2 cells each. Two CJK chars
		// fit into width 4; a third would push width to 6 and be
		// dropped before being emitted.
		{name: "CJK truncate stops on cell-width", s: "你好世", w: 4, want: "你好"}, //nolint:gosmopolitan // intentional Han literal
		{name: "CJK at exact width returns as-is", s: "你好", w: 4, want: "你好"},  //nolint:gosmopolitan // intentional Han literal
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := format.Truncate(tc.s, tc.w)
			require.Equal(t, tc.want, got)
			if tc.w > 0 {
				require.LessOrEqual(t, lipgloss.Width(got), tc.w,
					"Truncate must produce a string no wider than w cells")
			}
		})
	}
}

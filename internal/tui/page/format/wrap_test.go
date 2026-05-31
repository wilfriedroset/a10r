// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

func TestHanging(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		s           string
		width       int
		hangingCols int
		want        []string
	}{
		{
			name:  "fits on one line",
			s:     "short",
			width: 20,
			want:  []string{"short"},
		},
		{
			name:        "wraps at whitespace with hanging indent",
			s:           "alpha beta gamma",
			width:       7,
			hangingCols: 2,
			want:        []string{"alpha", "  beta", "  gamma"},
		},
		{
			name:        "hard-cuts a word longer than width",
			s:           "supercalifragilistic word",
			width:       6,
			hangingCols: 0,
			want:        []string{"superc", "alifra", "gilist", "ic", "word"},
		},
		{
			name:  "non-positive width returns input verbatim",
			s:     "anything goes",
			width: 0,
			want:  []string{"anything goes"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, format.Hanging(tc.s, tc.width, tc.hangingCols))
		})
	}
}

// TestHanging_IndentOnlyWhitespaceTerminates guards the forward-
// progress invariant: when the only whitespace falls inside the
// hanging indent, the naive last-whitespace break never shrinks the
// remainder and the loop spins forever. Every produced line must fit
// the width and the join must round-trip the content.
func TestHanging_IndentOnlyWhitespaceTerminates(t *testing.T) {
	t.Parallel()

	const width, hang = 5, 3
	got := format.Hanging("https://example.com/very/long/path", width, hang)
	require.Greater(t, len(got), 1, "a value wider than width must wrap")
	for _, line := range got {
		require.LessOrEqual(t, lipgloss.Width(line), width)
	}
	require.Equal(t,
		"https://example.com/very/long/path",
		strings.ReplaceAll(strings.Join(got, ""), " ", ""),
	)
}

func TestHardCut(t *testing.T) {
	t.Parallel()

	require.Equal(t, 3, format.HardCut("abcdef", 3))
	require.Equal(t, len("abc"), format.HardCut("abc", 10), "whole string fits under limit")
	require.Equal(t, 0, format.HardCut("abc", 0), "nothing fits in zero columns")
}

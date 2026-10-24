// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
		{
			// hangingCols >= width loops forever without this guard (fuzz corpus 7c0f6150e9d49953).
			name:        "indent wider than wrap width returns input verbatim",
			s:           "             HighCPU",
			width:       2,
			hangingCols: 13,
			want:        []string{"             HighCPU"},
		},
		{
			name:        "indent as wide as the width returns input verbatim",
			s:           "abcdef",
			width:       3,
			hangingCols: 3,
			want:        []string{"abcdef"},
		},
		{
			name:  "a rune wider than the width is emitted whole",
			s:     "\u4f60",
			width: 1,
			want:  []string{"\u4f60"},
		},
		{
			name:        "first line breaks at a space inside the indent columns",
			s:           "ab cdef",
			width:       4,
			hangingCols: 2,
			want:        []string{"ab", "  cd", "  ef"},
		},
		{
			name:  "rune wider than width is emitted whole but alone",
			s:     "\u4f60\u597d\u4e16\u754c",
			width: 1,
			want:  []string{"\u4f60", "\u597d", "\u4e16", "\u754c"},
		},
		{
			name:        "wide rune after the indent is emitted whole but alone",
			s:           "ab \u4f60\u597d",
			width:       3,
			hangingCols: 2,
			want:        []string{"ab", "  \u4f60", "  \u597d"},
		},
		{
			name:        "leading spaces before a long token do not emit a blank line",
			s:           "     abcdefghijklmnop",
			width:       10,
			hangingCols: 5,
			want:        []string{"     abcde", "     fghij", "     klmno", "     p"},
		},
		{
			name:        "negative indent wraps as no indent",
			s:           "abc def",
			width:       4,
			hangingCols: -1,
			want:        []string{"abc", "def"},
		},
		{
			name:        "emoji with variation selector is never split",
			s:           "ab ❤️❤️",
			width:       3,
			hangingCols: 1,
			want:        []string{"ab", " ❤️", " ❤️"},
		},
		{
			name:  "wide characters between narrow ones wrap whole",
			s:     "a❤️b❤️c",
			width: 2,
			want:  []string{"a", "❤️", "b", "❤️", "c"},
		},
		{
			name:  "flag of two regional indicators is never split",
			s:     "🇫🇷🇫🇷",
			width: 3,
			want:  []string{"🇫🇷", "🇫🇷"},
		},
		{
			name:        "control byte glued to an invalid byte still counts its width",
			s:           "0000\x00\xf1",
			width:       4,
			hangingCols: 2,
			want:        []string{"0000", "  \x00\xf1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, hangingWithin(t, tc.s, tc.width, tc.hangingCols))
		})
	}
}

// hangingWithin fails fast when Hanging stops terminating, instead of
// blocking until the go test timeout.
// The goroutine leaks on timeout; accepted because the test fails at once and fuzzing runs under a memory cap.
func hangingWithin(t *testing.T, s string, width, hangingCols int) []string {
	t.Helper()
	done := make(chan []string, 1)
	go func() { done <- format.Hanging(s, width, hangingCols) }()
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatalf("Hanging(%q, %d, %d) did not terminate", s, width, hangingCols)
		return nil
	}
}

func FuzzHanging(f *testing.F) {
	seeds := []struct {
		s                  string
		width, hangingCols int
	}{
		{"     abcdefghijklmnop", 10, 5},
		{"ab cdef", 4, 2},
		{"\u4f60\u597d\u4e16\u754c", 1, 0},
		{"ab \u4f60\u597d", 3, 2},
		{"abc def", 4, -1},
		{"             HighCPU", 2, 13},
		{"supercalifragilistic word", 6, 0},
		{"ab ❤️❤️", 3, 1},
		{"a❤️b❤️c", 2, 0},
		{"🇫🇷🇫🇷", 3, 0},
	}
	for _, sd := range seeds {
		f.Add(sd.s, sd.width, sd.hangingCols)
	}
	f.Fuzz(func(t *testing.T, s string, width, hangingCols int) {
		width, hangingCols = width%64, hangingCols%64
		got := hangingWithin(t, s, width, hangingCols)
		if width <= 0 || hangingCols >= width || lipgloss.Width(s) <= width {
			require.Equal(t, []string{s}, got, "verbatim passthrough")
			return
		}
		leading := len(s) - len(strings.TrimLeft(s, " "))
		for _, line := range got {
			if lipgloss.Width(line) > width {
				content := strings.TrimLeft(line, " ")
				first, _ := ansi.FirstGraphemeCluster(content, ansi.GraphemeWidth)
				require.Equal(t, content, first,
					"line %q wider than %d must hold a single character after its indent", line, width)
			}
			if strings.TrimLeft(line, " ") == "" {
				require.GreaterOrEqual(t, leading, width,
					"blank line %q only allowed when the input leads with a full row of spaces", line)
			}
		}
	})
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

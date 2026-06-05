// SPDX-License-Identifier: Apache-2.0

package format

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Hanging wraps s to width columns, indenting continuation lines by
// hangingCols. Word-wraps at whitespace, hard-cutting when a single
// word overflows — or, crucially, when the only whitespace sits inside
// the hanging indent, which would otherwise loop forever cutting only
// the indent and never the content.
func Hanging(s string, width, hangingCols int) []string {
	if width <= 0 {
		return []string{s}
	}
	if lipgloss.Width(s) <= width {
		return []string{s}
	}
	// hangingCols >= width: indent reconstruction already overflows the
	// limit on every iteration — loop can never make progress.
	if hangingCols >= width {
		return []string{s}
	}
	hang := strings.Repeat(" ", hangingCols)

	var out []string
	rest := s
	limit := width
	for lipgloss.Width(rest) > limit {
		cut := bestBreakIndex(rest, limit)
		// Forward-progress guard: a cut at/before the indent yields a
		// no-content line that never shrinks rest, so hard-cut instead.
		if cut <= hangingCols {
			cut = HardCut(rest, limit)
		}
		if cut <= 0 {
			break // pathological input; emit what we have
		}
		out = append(out, rest[:cut])
		rest = hang + strings.TrimLeft(rest[cut:], " ")
	}
	out = append(out, rest)
	return out
}

// HardCut returns the byte index at which s's leading slice stops
// fitting within limit columns — a mid-word break, used when no
// whitespace break is available.
func HardCut(s string, limit int) int {
	width := 0
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > limit {
			return i
		}
		width += rw
	}
	return len(s)
}

// bestBreakIndex returns the byte index to split s so the leading slice
// fits within limit columns, preferring the last whitespace at-or-before
// the limit and hard-cutting when a single word overflows.
func bestBreakIndex(s string, limit int) int {
	if lipgloss.Width(s) <= limit {
		return len(s)
	}
	width := 0
	lastWS := -1
	for i, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > limit {
			if lastWS > 0 {
				return lastWS
			}
			return i
		}
		if r == ' ' {
			lastWS = i
		}
		width += rw
	}
	return len(s)
}

// SPDX-License-Identifier: Apache-2.0

package format

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Hanging wraps s to width columns, indenting continuation lines by
// hangingCols. Word-wraps at whitespace, hard-cutting when a single
// word overflows — or, crucially, when the only whitespace sits inside
// the hanging indent, which would otherwise loop forever cutting only
// the indent and never the content. A character (grapheme cluster) is
// never split; one wider than the room left after the indent is emitted
// whole but alone on its line, the only case a line exceeds width.
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
	hang := strings.Repeat(" ", max(hangingCols, 0))

	var out []string
	rest := s
	// Capped below width so a full row of leading spaces still hard-cuts
	// within width instead of overflowing it.
	indent := min(len(s)-len(strings.TrimLeft(s, " ")), width-1)
	for lipgloss.Width(rest) > width {
		cut := bestBreakIndex(rest, width)
		// Forward-progress guard: a cut at/before the indent yields a
		// no-content line that never shrinks rest, so hard-cut instead.
		if cut <= indent {
			cut = HardCut(rest, width)
		}
		if cut <= indent {
			first, _ := ansi.FirstGraphemeCluster(rest[indent:], ansi.GraphemeWidth)
			cut = indent + len(first)
		}
		out = append(out, rest[:cut])
		tail := strings.TrimLeft(rest[cut:], " ")
		if tail == "" {
			return out
		}
		rest = hang + tail
		indent = len(hang)
	}
	return append(out, rest)
}

// HardCut returns the byte index at which s's leading slice stops
// fitting within limit columns — a mid-word break, used when no
// whitespace break is available. It never splits a character.
func HardCut(s string, limit int) int {
	width := 0
	for i := 0; i < len(s); {
		c, _ := ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
		// lipgloss.Width, not the cluster's own width, so a cluster glued to a control byte matches what Hanging measures.
		cw := lipgloss.Width(c)
		if width+cw > limit {
			return i
		}
		width += cw
		i += len(c)
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
	for i := 0; i < len(s); {
		c, _ := ansi.FirstGraphemeCluster(s[i:], ansi.GraphemeWidth)
		cw := lipgloss.Width(c)
		if width+cw > limit {
			if lastWS > 0 {
				return lastWS
			}
			return i
		}
		if c == " " {
			lastWS = i
		}
		width += cw
		i += len(c)
	}
	return len(s)
}

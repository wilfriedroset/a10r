// SPDX-License-Identifier: Apache-2.0

// Package format holds width-aware text helpers shared across pages
// and chrome packages: cell padding and width-bounded truncation
// against lipgloss.Width (which counts terminal cells, not bytes,
// honouring CJK / emoji width).
package format

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// PadRight pads s with trailing spaces so the rendered string is
// exactly w terminal cells wide. Returns "" if w <= 0. Strings
// whose terminal-cell width already meets or exceeds w are
// truncated to w via Truncate — the function never returns a
// string wider than the requested width.
//
// Not SGR-aware (delegates to Truncate); see Truncate's caveat.
func PadRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	cur := lipgloss.Width(s)
	if cur >= w {
		return Truncate(s, w)
	}
	return s + strings.Repeat(" ", w-cur)
}

// Truncate clips s so its rendered terminal-cell width is at most
// w, walking runes and counting per-rune cell width via
// lipgloss.Width. Returns "" if w <= 0; returns s unchanged when
// it already fits.
//
// Not SGR-aware: a string carrying ANSI escape sequences will have
// the escape bytes counted as ordinary runes, mid-truncation can
// land inside an unterminated escape. For pre-styled input use
// internal/tui/help.truncateVisible (kept package-local until a
// second consumer arrives).
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > w {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// runeWidth returns r's terminal-cell width, biased for the common
// ASCII case. Printable ASCII (0x20-0x7E) is unconditionally width 1
// — matches lipgloss.Width on every supported terminal and avoids
// the per-rune string(r) allocation + Width call that dominates
// table-cell rendering at thousands of cells per frame. Non-ASCII
// (control chars, CJK, emoji) falls through to lipgloss so width
// stays correct on every input the fast path would mis-classify.
func runeWidth(r rune) int {
	if r >= 0x20 && r < 0x7F {
		return 1
	}
	return lipgloss.Width(string(r))
}

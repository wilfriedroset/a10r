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
// SGRTruncate.
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

// SGRTruncate clips s to at most w visible cells while keeping ANSI
// (SGR) escape sequences intact. Where Truncate counts every byte as
// content and may slice mid-escape, SGRTruncate skips over the bytes
// between ESC and the terminator `m`, copying them verbatim into the
// output without consuming budget. Use this for input that carries
// styling — pre-styled body lines, lipgloss-rendered cells embedded
// in a wider clamp.
//
// When truncation lands inside a styled run, SGRTruncate appends an
// explicit `\x1b[0m` reset so the active style does not bleed into
// the surrounding chrome. Today's chrome callers (panel.RenderBody,
// help.padRight) wrap the result in their own styled segment, which
// would override most leaks — but a future caller without that
// wrapper would otherwise see the next column tinted by the cut
// style. Defensive insurance.
//
// Returns "" for w <= 0; returns s unchanged when its visible width
// already fits.
func SGRTruncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + sgrResetLen)
	used := 0
	inEsc := false
	sawSGR := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
			sawSGR = true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
		default:
			rw := runeWidth(r)
			if used+rw > w {
				if sawSGR {
					b.WriteString(sgrReset)
				}
				return b.String()
			}
			b.WriteRune(r)
			used += rw
		}
	}
	return b.String()
}

const (
	sgrReset    = "\x1b[0m"
	sgrResetLen = len(sgrReset)
)

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

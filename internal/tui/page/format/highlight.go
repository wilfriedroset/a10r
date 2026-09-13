// SPDX-License-Identifier: Apache-2.0

package format

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// Highlighter paints the characters that made a row survive the
// active `/` filter. Spans reports the byte ranges of a cell that
// matched — footer.Matcher.MatchSpans is the reporter every page
// wires in, passed as a function so this package keeps no dependency
// on the filter grammar. Match wraps one matched run in the page's
// highlight treatment. The zero value paints nothing, which is what
// a page with no filter set hands to its renderer.
type Highlighter struct {
	Spans func(lowered string) [][2]int
	Match func(string) string
}

// HighlighterFor builds the painter for one row, the single place
// the three list pages agree on what a match looks like. A plain row
// takes the filter colour on the matched characters. A row the page
// wraps in one style takes an underline instead, so the row colour
// and the cursor background are left alone.
func HighlighterFor(spans func(string) [][2]int, match lipgloss.Style, rowStyled bool) Highlighter {
	if rowStyled {
		return Highlighter{Spans: spans, Match: Emphasis}
	}
	return Highlighter{Spans: spans, Match: MatchStyle(match)}
}

// Text renders one cell that carries no style of its own and returns
// it untouched when nothing matched. The table draws hundreds of such
// cells per frame, most of them with no filter active at all, so the
// no-match path must not reach lipgloss: a zero-style Render costs
// about 800 ns whatever the input.
func (h Highlighter) Text(text string) string {
	spans := h.spansOf(text)
	if len(spans) == 0 {
		return text
	}
	return h.compose(text, spans, identity)
}

// Cell renders one table cell: base outside the matched ranges, Match
// inside them. The two styles sit side by side and are never nested,
// because a nested style ends at the inner reset and leaves the rest
// of the cell unstyled.
func (h Highlighter) Cell(text string, base lipgloss.Style) string {
	spans := h.spansOf(text)
	if len(spans) == 0 {
		return base.Render(text)
	}
	return h.compose(text, spans, MatchStyle(base))
}

// compose walks the cell once, handing the unmatched runs to plain and
// the matched ones to Match.
//
// Callers pad or cut the cell to its column width first, so the paint
// stops at the last content byte: a range past it is dropped and a
// range crossing it ends there. That keeps the rendered width fixed,
// and it keeps a trailing `.*` from underlining the column gap.
func (h Highlighter) compose(text string, spans [][2]int, plain func(string) string) string {
	var b strings.Builder
	b.Grow(len(text) * 2)
	limit := len(strings.TrimRight(text, " "))
	prev := 0
	for _, s := range spans {
		if s[0] < prev || s[0] >= limit {
			continue
		}
		end := min(s[1], limit)
		writeRun(&b, plain, text[prev:s[0]])
		b.WriteString(h.Match(text[s[0]:end]))
		prev = end
	}
	writeRun(&b, plain, text[prev:])
	return b.String()
}

// spansOf reports the ranges to paint in text, after the two checks
// that make byte offsets trustworthy.
func (h Highlighter) spansOf(text string) [][2]int {
	if h.Spans == nil || h.Match == nil || text == "" {
		return nil
	}
	if strings.IndexByte(text, escape) >= 0 {
		// A cell its producer already styled carries escape bytes the
		// matcher never saw, so an offset into it can land inside an
		// escape sequence. Such a cell is highlighted at the site that
		// styled it, where the text is still plain.
		return nil
	}
	for _, r := range text {
		if r < utf8.RuneSelf {
			continue
		}
		if r == utf8.RuneError {
			// A byte that is not valid UTF-8 decodes to RuneError here
			// but expands to the three-byte U+FFFD in the lower-cased
			// copy, so every later offset shifts.
			return nil
		}
		if utf8.RuneLen(unicode.ToLower(r)) != utf8.RuneLen(r) {
			// Case folding moves bytes for a handful of runes (Turkish
			// dotted I among them). Every offset past such a rune would
			// shift, so the cell renders unmarked rather than wrong. The
			// test is per rune because one rune that grows and one that
			// shrinks cancel out in the total length.
			return nil
		}
	}
	return h.Spans(strings.ToLower(text))
}

// MatchStyle adapts a lipgloss style to the Match signature. A method
// value cannot be used directly: Render takes a variadic argument.
func MatchStyle(st lipgloss.Style) func(string) string {
	return func(s string) string { return st.Render(s) }
}

func identity(s string) string { return s }

func writeRun(b *strings.Builder, plain func(string) string, s string) {
	if s == "" {
		return
	}
	b.WriteString(plain(s))
}

const escape = 0x1b

const (
	emphasisOn  = "\x1b[4m"
	emphasisOff = "\x1b[24m"
)

// Emphasis marks a matched run on a row the page wrapped in a single
// row-level style — the cursor row, a marked row, a dimmed row.
// Underline switches off with its own code (SGR 24) rather than a
// reset, so the row colour survives the run. A lipgloss style here
// would emit a reset and leave the rest of the row unpainted.
//
// Underline alone, no bold: the cursor row is bold already (k9s
// parity, Table.Cursor), and SGR 22 means normal intensity, not "undo
// the bold I just set" — closing a bold run would switch the row's own
// bold off for everything after the match.
func Emphasis(s string) string { return emphasisOn + s + emphasisOff }

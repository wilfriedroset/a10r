// SPDX-License-Identifier: Apache-2.0

// Package filter holds the `/` filter language: the mode classifier
// and the compiled predicate for one buffer. It carries no chrome and
// imports nothing from internal/tui, so the language layer sits below
// the widgets that render it.
//
// The edge to internal/tui/filterexpr is one-directional: filterexpr
// calls NewMatcher for a free-text term, and this package must never
// import filterexpr in return.
package filter

import "strings"

// SearchMode is the matcher class for a `/` filter buffer. Four of
// the five values are auto-detected from the buffer; SearchExpression
// is the one only the page can decide. The detection is the lfk
// four-mode rule: explicit prefix sigils win, otherwise we look at
// the body for regex metacharacters and only flip to regex on a
// strong signal — a single `.` keeps the substring default because
// `web.api`-style dotted names are the most common false-positive
// in alert filtering.
//
// The rule is deliberately separate from how a page applies the
// filter: this layer only classifies the buffer, callers are
// responsible for the actual matcher (substring, fuzzy, regex).
type SearchMode int

const (
	// SearchSubstring is the default — case-insensitive substring
	// over the same row projection the page already uses for the
	// plain `/` filter. Single `.` and other "looks regex but
	// probably isn't" inputs land here.
	SearchSubstring SearchMode = iota
	// SearchFuzzy is the `~`-prefixed mode. The matcher input is
	// the buffer with the leading `~` stripped.
	SearchFuzzy
	// SearchLiteral is the `\`-prefixed mode. Same matcher as
	// substring but lets the user opt out of regex auto-detection
	// when the body would otherwise trip the meta threshold —
	// useful for searching label values that legitimately contain
	// regex glyphs (e.g. `\(svc).+` to match the literal text).
	SearchLiteral
	// SearchRegex flips on when the body contains at least two
	// distinct regex metacharacters. The matcher input is the
	// buffer as-is (no stripping); compilation failures are the
	// caller's problem to surface.
	SearchRegex
	// SearchExpression is the boolean-expression grammar. It is the
	// one mode DetectSearchMode never returns: the grammar is page-
	// conditional (listpage.Base.FilterReadsExpr), so the caller that
	// knows the page decides, via filterexpr.Compile, and reads the
	// label from here.
	SearchExpression
)

// String returns a stable lower-case label suitable for logs and
// chrome hints. The labels are part of the user-facing contract
// (keybindings.md cites them), so renaming requires a doc update.
func (m SearchMode) String() string {
	switch m {
	case SearchFuzzy:
		return "fuzzy"
	case SearchLiteral:
		return "literal"
	case SearchRegex:
		return "regex"
	case SearchExpression:
		return "expr"
	default:
		return "substring"
	}
}

// regexMetas is the set of metacharacters DetectSearchMode counts
// when deciding whether a buffer looks like a regex. The list
// covers the RE2 surface a casual user would reach for — anchors,
// quantifiers, alternation, character classes, groups — plus the
// escape rune so `\d`-style escapes count as a meta even when the
// `\` is not a leading sigil. We deliberately omit `{` / `}` and
// `-`: they show up in plain alert names ("ms-1.2-3") and hyphen-
// separated label values often enough that counting them would
// flip too aggressively.
var regexMetas = map[rune]struct{}{
	'.':  {},
	'*':  {},
	'+':  {},
	'?':  {},
	'[':  {},
	']':  {},
	'(':  {},
	')':  {},
	'|':  {},
	'^':  {},
	'$':  {},
	'\\': {},
}

// regexMetaThreshold is the minimum number of distinct metas a
// buffer must contain before DetectSearchMode flips to regex. Two
// is the lfk default and the empirical sweet spot: a single `.`
// stays substring (so `web.api` doesn't surprise the user), but
// `web.*api` or `^web` flip immediately.
const regexMetaThreshold = 2

// DetectSearchMode classifies a `/` filter buffer per the lfk
// four-mode rule:
//
//   - "" -> substring (the empty buffer has no meaningful mode;
//     callers should treat it as "no filter applied").
//   - leading `~` -> fuzzy.
//   - leading `\` -> literal (also a manual escape hatch when the
//     body would otherwise trigger regex auto-detect).
//   - body with >=regexMetaThreshold distinct regex metas ->
//     regex.
//   - otherwise -> substring.
//
// The function is pure and side-effect-free; the prefix sigils are
// classification only — callers strip them via TrimSearchPrefix
// when they need the matcher input.
func DetectSearchMode(input string) SearchMode {
	if input == "" {
		return SearchSubstring
	}
	// Both sigils are single-byte ASCII (`~` 0x7E, `\` 0x5C), so
	// indexing the first byte is safe regardless of what comes
	// after — multi-byte runes start with a byte >= 0xC0 and can
	// never collide with the prefix check.
	switch input[0] {
	case '~':
		return SearchFuzzy
	case '\\':
		return SearchLiteral
	}
	seen := make(map[rune]struct{}, regexMetaThreshold)
	for _, r := range input {
		if _, ok := regexMetas[r]; !ok {
			continue
		}
		seen[r] = struct{}{}
		if len(seen) >= regexMetaThreshold {
			return SearchRegex
		}
	}
	return SearchSubstring
}

// TrimSearchPrefix returns the matcher input for a buffer — the
// raw text the caller should hand to the substring/fuzzy/regex
// engine. It strips the single leading sigil that flagged the
// mode (`~` for fuzzy, `\` for literal) and leaves every other
// input untouched. The returned mode is the one DetectSearchMode
// would have classified the original buffer as, so callers get
// both pieces from a single call site without re-running the
// detection.
func TrimSearchPrefix(input string) (mode SearchMode, matcher string) {
	mode = DetectSearchMode(input)
	switch mode {
	case SearchFuzzy:
		return mode, strings.TrimPrefix(input, "~")
	case SearchLiteral:
		return mode, strings.TrimPrefix(input, "\\")
	case SearchSubstring, SearchRegex, SearchExpression:
		return mode, input
	}
	return mode, input
}

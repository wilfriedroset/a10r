// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"regexp"
	"strings"
)

// Matcher is the compiled-once predicate for a `/`-prompt buffer.
// Pages call NewMatcher with their stored p.filter and apply
// Match per row instead of hand-rolling strings.Contains — the
// classifier picks substring / fuzzy / literal / regex from the
// buffer itself, no toggle key required.
//
// Matcher is a value type and is safe to copy. The compiled regex
// (when present) is shared, but *regexp.Regexp is documented as
// safe for concurrent use, so passing the matcher by value into
// inner loops doesn't mean each row paying for a recompile.
type Matcher struct {
	mode   SearchMode
	needle string         // lower-cased matcher input, post-prefix-strip
	re     *regexp.Regexp // populated only in regex mode
	// needleRunes caches the rune-decoded needle for the fuzzy path.
	// Decoding once at construction keeps Match allocation-free —
	// the alternative ([]rune in fuzzyMatch) would alloc per row,
	// once for every entry the recompute walks.
	needleRunes []rune
	// matchAll is true for the empty-buffer fast path. The page-level
	// filter functions already have their own "no filter set" early
	// return, but plumbing it through Matcher too means a caller that
	// happens to construct one with "" still gets the universal-match
	// behaviour rather than an awkward "is needle empty" branch on
	// the hot path.
	matchAll bool
}

// NewMatcher classifies input and compiles the predicate. Empty
// buffer yields a match-everything matcher. A regex-mode buffer
// that fails to compile downgrades to substring on the original
// text — the user sees something matching while they type rather
// than the view freezing on the last good keystroke.
//
// The needle is lower-cased once at construction so Match can stay
// allocation-free per row. Callers feed Match a haystack they have
// already lower-cased (the page-level lowerComposite cache is the
// canonical example).
func NewMatcher(input string) Matcher {
	if input == "" {
		return Matcher{matchAll: true}
	}
	mode, raw := TrimSearchPrefix(input)
	switch mode {
	case SearchRegex:
		// Case-insensitive by default to mirror the substring path —
		// users coming from `/foo` don't expect Capital sensitivity to
		// swap in just because the body looked regex-y. (?i) is the
		// RE2 flag for the whole pattern; positional flags inside the
		// user's buffer still work because they're prefix-additive.
		re, err := regexp.Compile("(?i)" + raw)
		if err != nil {
			// Compilation failed — fall back to substring on the raw
			// (un-stripped) buffer so the user keeps seeing live
			// feedback rather than a frozen view.
			return Matcher{mode: SearchSubstring, needle: strings.ToLower(input)}
		}
		return Matcher{mode: SearchRegex, re: re}
	case SearchFuzzy:
		needle := strings.ToLower(raw)
		return Matcher{mode: SearchFuzzy, needle: needle, needleRunes: []rune(needle)}
	case SearchLiteral, SearchSubstring:
		return Matcher{mode: mode, needle: strings.ToLower(raw)}
	}
	return Matcher{mode: SearchSubstring, needle: strings.ToLower(raw)}
}

// Mode returns the detected search mode for header / chrome
// rendering. Empty buffers report substring (the safe default).
func (m Matcher) Mode() SearchMode {
	if m.matchAll {
		return SearchSubstring
	}
	return m.mode
}

// MatchAll reports whether the matcher accepts every input. True
// for the empty buffer; pages can use it to skip the per-row loop
// entirely and hand the input slice straight to the view.
func (m Matcher) MatchAll() bool { return m.matchAll }

// Match reports whether haystack — already lower-cased by the
// caller — matches the compiled predicate. The fuzzy path takes
// the same lower-cased haystack because the needle is lower-cased
// at construction; using a uniform lower-vs-lower compare keeps
// the predicate allocation-free per row.
//
// Regex mode also reads the lower-cased haystack: the pattern is
// compiled with (?i), so the case-insensitivity is symmetric.
func (m Matcher) Match(haystack string) bool {
	if m.matchAll {
		return true
	}
	switch m.mode {
	case SearchRegex:
		return m.re.MatchString(haystack)
	case SearchFuzzy:
		return fuzzyMatch(m.needleRunes, haystack)
	case SearchSubstring, SearchLiteral:
		return strings.Contains(haystack, m.needle)
	}
	return false
}

// fuzzyMatch reports whether every rune in needle appears in
// haystack in order (not necessarily contiguous). The needle is
// pre-decoded by NewMatcher (m.needleRunes) so the inner loop
// walks haystack runes only — allocation-free and O(len(haystack)).
//
// This is the boolean predicate sahilm/fuzzy.Find computes as a
// side effect of its scoring pass; rolling it ourselves avoids the
// per-row Matches slice + sort cost the library pays to support
// ranking, which we don't use here (the page already owns its
// sort key).
func fuzzyMatch(needle []rune, haystack string) bool {
	if len(needle) == 0 {
		return true
	}
	ni := 0
	for _, hr := range haystack {
		if hr == needle[ni] {
			ni++
			if ni == len(needle) {
				return true
			}
		}
	}
	return false
}

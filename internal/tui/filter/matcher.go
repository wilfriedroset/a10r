// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"errors"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
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
// that fails to compile yields the regexp/syntax error AND a
// substring matcher over the original text: the fallback is the
// caller's choice, not a silent degrade. Hot recompute paths keep
// taking it so the view stays live while the user types; chrome that
// reports the buffer back to the user reads the error instead, via
// RegexErrText.
//
// The needle is lower-cased once at construction so Match can stay
// allocation-free per row. Callers feed Match a haystack they have
// already lower-cased (the page-level lowerComposite cache is the
// canonical example).
func NewMatcher(input string) (Matcher, error) {
	if input == "" {
		return Matcher{matchAll: true}, nil
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
			// Bare on purpose: the chrome names the grammar itself
			// (listpage.FilterError tags it `regex:`), so a wrap here
			// would double that in every surface that reports it.
			//nolint:wrapcheck // see above
			return Matcher{mode: SearchSubstring, needle: strings.ToLower(input)}, err
		}
		return Matcher{mode: SearchRegex, re: re}, nil
	case SearchFuzzy:
		needle := strings.ToLower(raw)
		return Matcher{mode: SearchFuzzy, needle: needle, needleRunes: []rune(needle)}, nil
	case SearchLiteral, SearchSubstring:
		return Matcher{mode: mode, needle: strings.ToLower(raw)}, nil
	}
	return Matcher{mode: SearchSubstring, needle: strings.ToLower(raw)}, nil
}

// RegexErrText renders err for a title tag that has one line of
// room: the syntax error's own decoration — Go's `error parsing
// regexp: ` prefix and the trailing echo of the pattern — is traded
// for that room, since the buffer is already on screen in the title's
// `</…>` segment and the echo would show the internally rewritten
// form (`(?i)…`, `^(?:…)$`) rather than what the user typed. Wrapper
// context around the syntax error survives.
func RegexErrText(err error) string {
	if err == nil {
		return ""
	}
	var se *syntax.Error
	if !errors.As(err, &se) {
		return err.Error()
	}
	return strings.Replace(err.Error(), se.Error(), se.Code.String(), 1)
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

// MatchSpans returns the byte ranges of haystack — already
// lower-cased by the caller, as Match takes it — that made the
// predicate say yes, or nil when it said no. The renderer paints
// those ranges in the filter colour.
//
// Substring and literal report the first occurrence, regex reports
// every occurrence, and fuzzy reports one range per matched rune with
// adjacent runes merged into a single range. Match stays the hot-path
// predicate: pages call MatchSpans for the visible window only, never
// for the whole list.
func (m Matcher) MatchSpans(haystack string) [][2]int {
	if m.matchAll {
		return nil
	}
	switch m.mode {
	case SearchRegex:
		return regexSpans(m.re, haystack)
	case SearchFuzzy:
		return fuzzySpans(m.needleRunes, haystack)
	case SearchSubstring, SearchLiteral:
		if m.needle == "" {
			return nil
		}
		if i := strings.Index(haystack, m.needle); i >= 0 {
			return [][2]int{{i, i + len(m.needle)}}
		}
	}
	return nil
}

// regexSpans converts FindAllStringIndex output and drops zero-width
// matches: a pattern like `^|$` matches an empty range the renderer
// cannot paint, and keeping it would split a cell for nothing.
func regexSpans(re *regexp.Regexp, haystack string) [][2]int {
	found := re.FindAllStringIndex(haystack, -1)
	out := make([][2]int, 0, len(found))
	for _, f := range found {
		if f[0] == f[1] {
			continue
		}
		out = append(out, [2]int{f[0], f[1]})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fuzzySpans is fuzzyMatch with the positions kept: the ranges of the
// runes that consumed the needle, merged when they sit back to back
// so a contiguous hit paints as one run rather than per character.
// A needle that never completes reports nil, so the partial ranges
// walked so far are discarded — the row did not match.
func fuzzySpans(needle []rune, haystack string) [][2]int {
	if len(needle) == 0 {
		return nil
	}
	out := make([][2]int, 0, len(needle))
	ni := 0
	for i, hr := range haystack {
		if hr != needle[ni] {
			continue
		}
		end := i + utf8.RuneLen(hr)
		if last := len(out) - 1; last >= 0 && out[last][1] == i {
			out[last][1] = end
		} else {
			out = append(out, [2]int{i, end})
		}
		ni++
		if ni == len(needle) {
			return out
		}
	}
	return nil
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

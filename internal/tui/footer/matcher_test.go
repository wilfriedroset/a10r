// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMatcher_ModeAndMatch is the canonical happy-path table. Each
// row pins one of the four modes and asserts both the classified
// mode and a representative positive / negative match against a
// lower-cased haystack — same shape pages will feed.
func TestMatcher_ModeAndMatch(t *testing.T) {
	t.Parallel()

	const haystack = "highcpu\x00warning\x00web.api\x00prod"

	cases := []struct {
		name      string
		input     string
		wantMode  SearchMode
		wantMatch bool
	}{
		{"empty matches everything", "", SearchSubstring, true},
		{"plain substring hit", "warning", SearchSubstring, true},
		{"plain substring miss", "nope", SearchSubstring, false},
		{"single dot stays substring (web.api)", "web.api", SearchSubstring, true},
		{"single dot substring miss", "web.gone", SearchSubstring, false},
		{"fuzzy hit on subsequence", "~hgcpu", SearchFuzzy, true},
		{"fuzzy miss when subsequence breaks", "~xyz", SearchFuzzy, false},
		{"literal substring keeps body verbatim", `\web.api`, SearchLiteral, true},
		{"literal substring miss", `\nope`, SearchLiteral, false},
		{"regex matches dot-star", ".*api", SearchRegex, true},
		{"regex anchor plus star matches", "^high.*", SearchRegex, true},
		{"regex anchor plus star miss", "^nope.*", SearchRegex, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMatcher(tc.input)
			require.Equal(t, tc.wantMode, m.Mode())
			require.Equal(t, tc.wantMatch, m.Match(haystack))
		})
	}
}

// TestMatcher_MatchAllShortCircuits pins the empty-buffer fast
// path: callers who hold an empty filter can skip the per-row
// loop entirely. The Mode() falls back to SearchSubstring so
// chrome that reads the mode for header text doesn't have to
// special-case empty.
func TestMatcher_MatchAllShortCircuits(t *testing.T) {
	t.Parallel()

	m := NewMatcher("")
	require.True(t, m.MatchAll())
	require.Equal(t, SearchSubstring, m.Mode())
	require.True(t, m.Match("anything goes"))
	require.True(t, m.Match(""))
}

// TestMatcher_LiteralEscapesRegexBody pins the documented escape
// hatch: a leading `\` short-circuits the regex auto-detect even
// when the body would have tripped the meta threshold. The point
// is that `\(prod|stg)` is parsed as the literal string
// `(prod|stg)`, not as a regex alternation group.
func TestMatcher_LiteralEscapesRegexBody(t *testing.T) {
	t.Parallel()

	m := NewMatcher(`\(prod|stg)`)
	require.Equal(t, SearchLiteral, m.Mode())
	// Hits the literal text.
	require.True(t, m.Match("alert (prod|stg) detail"))
	// Does NOT match either alternative as a regex would have.
	require.False(t, m.Match("only-prod"))
}

// TestMatcher_RegexFallsBackOnCompileFailure pins the safety net.
// When the body trips the meta threshold but is unparseable, we
// downgrade to substring on the original input rather than
// freezing the view on the last-good keystroke. The user keeps
// seeing live feedback while they finish typing.
func TestMatcher_RegexFallsBackOnCompileFailure(t *testing.T) {
	t.Parallel()

	// `[abc` has two distinct metas (`[` and `c` doesn't count, but
	// the `[` plus the unmatched-bracket compilation error trips the
	// fallback path). Using `(*` to make compile failure deterministic.
	m := NewMatcher("(*+")
	require.Equal(t, SearchSubstring, m.Mode())
	// Substring on the lower-cased original input — pages pass
	// lower-cased haystacks, so we expect the literal characters
	// to match.
	require.True(t, m.Match("foo (*+ bar"))
	require.False(t, m.Match("nope"))
}

// TestMatcher_RegexIsCaseInsensitive pins the (?i) prefix the
// constructor injects — substring-mode is case-insensitive (the
// page lower-cases its haystack), and regex-mode follows suit so
// `^WEB` and `^web` both light up the same row.
func TestMatcher_RegexIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	m := NewMatcher("^web.*api")
	require.Equal(t, SearchRegex, m.Mode())
	// Pages feed lower-cased haystacks; the (?i) flag means an
	// upper-case pattern would still match against the lower body.
	require.True(t, m.Match("web service api"))
}

// TestFuzzyMatch_RuneSafe pins the multibyte safety of the inline
// subsequence checker — the matcher operates on runes, not bytes,
// so a needle-rune that happens to share a leading byte with a
// haystack-rune doesn't false-positive.
func TestFuzzyMatch_RuneSafe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		needle  string
		haystck string
		want    bool
	}{
		{"ascii subsequence hit", "abc", "a-b-c", true},
		{"ascii subsequence miss when out-of-order", "cba", "a-b-c", false},
		{"empty needle always matches", "", "anything", true},
		{"unicode rune subsequence hit", "café", "le café noir", true},
		{"unicode rune subsequence miss", "cofé", "le café noir", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, fuzzyMatch([]rune(tc.needle), tc.haystck))
		})
	}
}

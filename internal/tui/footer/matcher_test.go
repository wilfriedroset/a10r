// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"errors"
	"fmt"
	"regexp/syntax"
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
			m, err := NewMatcher(tc.input)
			require.NoError(t, err)
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

	m, err := NewMatcher("")
	require.NoError(t, err)
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

	m, err := NewMatcher(`\(prod|stg)`)
	require.NoError(t, err)
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
	m, err := NewMatcher("(*+")
	require.Error(t, err)
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

	m, err := NewMatcher("^web.*api")
	require.NoError(t, err)
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

// TestNewMatcher_CompileError pins the error contract the chrome
// builds on: a regex-mode buffer that will not compile hands back the
// regexp/syntax error unwrapped, so callers can inspect it, alongside
// a usable substring fallback for the hot recompute paths that ignore
// the error and keep the view live.
func TestNewMatcher_CompileError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    string
		wantCode syntax.ErrorCode
	}{
		{name: "half-typed group", input: "^web(", wantCode: syntax.ErrMissingParen},
		{name: "unbalanced character class", input: "^web[a", wantCode: syntax.ErrMissingBracket},
		{name: "valid two-meta pattern", input: "^web.*"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewMatcher(tc.input)
			if tc.wantCode == "" {
				require.NoError(t, err)
				require.Equal(t, SearchRegex, m.Mode())
				return
			}
			var se *syntax.Error
			require.ErrorAs(t, err, &se, "the regexp/syntax error survives for errors.As")
			require.Equal(t, tc.wantCode, se.Code)
			require.Equal(t, SearchSubstring, m.Mode(),
				"the fallback matcher is still usable by callers that ignore the error")
		})
	}
}

// TestRegexErrText pins the chrome rendering: the syntax error's own
// decoration goes (Go's prefix and the echo of the internally
// rewritten pattern), any wrapper context stays, and a buffer that
// happens to contain the prefix as literal text is not mangled.
func TestRegexErrText(t *testing.T) {
	t.Parallel()

	_, compileErr := NewMatcher("^web(")

	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error renders empty"},
		{name: "bare compile error", err: compileErr, want: "missing closing )"},
		{
			name: "wrapper context survives",
			err:  fmt.Errorf("compile regex %q: %w", "(", compileErr),
			want: `compile regex "(": missing closing )`,
		},
		{
			name: "a non-syntax error passes through verbatim",
			err:  errors.New("error parsing regexp: not really"),
			want: "error parsing regexp: not really",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, RegexErrText(tc.err))
		})
	}
}

// TestMatcher_MatchSpans pins the byte ranges each mode reports for
// the renderer's highlight. The haystack is the lower-cased shape a
// page cell carries; a miss reports no span at all so the caller can
// skip the row without a second predicate call.
func TestMatcher_MatchSpans(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		input    string
		haystack string
		want     [][2]int
	}{
		{"empty buffer highlights nothing", "", "highcpu", nil},
		{"substring reports the hit", "cpu", "highcpu", [][2]int{{4, 7}}},
		{"substring miss reports nothing", "disk", "highcpu", nil},
		{"substring reports the first hit only", "cpu", "cpu cpu", [][2]int{{0, 3}}},
		{"literal keeps the body verbatim", `\web.api`, "web.api up", [][2]int{{0, 7}}},
		{"literal miss reports nothing", `\web.api`, "webxapi", nil},
		{"regex reports every hit", "a.*?b|c", "ab c", [][2]int{{0, 2}, {3, 4}}},
		{"regex miss reports nothing", "^zz.*", "ab c", nil},
		{"fuzzy reports one span per run", "~hcp", "high cpu", [][2]int{{0, 1}, {5, 7}}},
		{"fuzzy merges adjacent runes", "~hig", "high cpu", [][2]int{{0, 3}}},
		{"fuzzy miss reports nothing", "~xyz", "high cpu", nil},
		{"multi-byte substring spans whole runes", "éé", "aééb", [][2]int{{1, 5}}},
		{"multi-byte fuzzy merges adjacent runes", "~éé", "aééb", [][2]int{{1, 5}}},
		{"multi-byte fuzzy splits on a gap", "~éé", "éxé", [][2]int{{0, 2}, {3, 5}}},
		{"bare fuzzy sigil highlights nothing", "~", "high cpu", nil},
		{"bare literal sigil highlights nothing", `\`, "high cpu", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := NewMatcher(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.want, m.MatchSpans(tc.haystack))
		})
	}
}

// TestMatcher_MatchSpansAgreesWithMatch guards the pair against
// drifting apart: a span list is the renderer's proof that Match
// said yes, so a reported span must never contradict the predicate
// the recompute filtered on. The reverse does not hold — a bare
// sigil matches every row and has nothing to paint.
func TestMatcher_MatchSpansAgreesWithMatch(t *testing.T) {
	t.Parallel()

	const haystack = "highcpu\x00warning\x00web.api\x00prod"
	for _, input := range []string{"warning", "nope", "~hgcpu", "~xyz", `\web.api`, ".*api", "^nope.*"} {
		m, err := NewMatcher(input)
		require.NoError(t, err)
		require.Equal(t, m.Match(haystack), m.MatchSpans(haystack) != nil, "input %q", input)
	}
	for _, input := range []string{"~", `\`} {
		m, err := NewMatcher(input)
		require.NoError(t, err)
		require.True(t, m.Match(haystack), "input %q", input)
		require.Nil(t, m.MatchSpans(haystack), "input %q", input)
	}
}

// TestMatcher_MatchSpansZeroWidthRegex drops empty matches: a
// zero-width span would style nothing and the renderer would still
// pay for the segment split.
func TestMatcher_MatchSpansZeroWidthRegex(t *testing.T) {
	t.Parallel()

	m, err := NewMatcher("^|$")
	require.NoError(t, err)
	require.Equal(t, SearchRegex, m.Mode())
	require.Nil(t, m.MatchSpans("abc"))
}

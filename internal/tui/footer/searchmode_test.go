// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDetectSearchMode is the canonical happy-path table for the
// four-mode classifier. Every named branch of the lfk rule shows
// up at least once: explicit prefix sigils, the regex meta
// threshold, and the empty-buffer fast path.
func TestDetectSearchMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  SearchMode
	}{
		{"empty buffer is substring", "", SearchSubstring},
		{"plain word stays substring", "diskfull", SearchSubstring},
		{"tilde prefix flips to fuzzy", "~dskfll", SearchFuzzy},
		{"backslash prefix flips to literal", `\web.api`, SearchLiteral},
		{"two distinct metas flip to regex", "web.*api", SearchRegex},
		{"caret anchor plus star flips to regex", "^web.*", SearchRegex},
		{"alternation plus group flips to regex", "(prod|stg)", SearchRegex},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, DetectSearchMode(tc.input))
		})
	}
}

// TestDetectSearchMode_SubstringRegexBoundary pins the
// substring-vs-regex edge that the plan calls out as canonical:
// a single `.` is still substring (so `web.api` and `1.2.3`
// don't surprise the user), but a second distinct meta in the
// same buffer flips to regex. The boundary is the load-bearing
// invariant of the whole detection rule, so it gets its own
// table separate from the broad happy-path coverage.
func TestDetectSearchMode_SubstringRegexBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  SearchMode
	}{
		{"single dot stays substring", "web.api", SearchSubstring},
		{"repeated same meta stays substring", "1.2.3.4", SearchSubstring},
		{"single star stays substring", "abc*", SearchSubstring},
		{"single bracket stays substring", "list[", SearchSubstring},
		{"dot plus star flips to regex", "web.*", SearchRegex},
		{"dot plus dollar flips to regex", "log.$", SearchRegex},
		{"bracket pair flips to regex", "[ab]", SearchRegex},
		// `\d*` is the documented escape hatch — the leading `\`
		// claims literal mode before meta counting runs, so the
		// body never gets inspected. Pinning this row here makes
		// the prefix-vs-body precedence explicit on the same
		// table that owns the meta threshold.
		{"backslash prefix shadows body metas", `\d*`, SearchLiteral},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, DetectSearchMode(tc.input))
		})
	}
}

// TestTrimSearchPrefix covers the prefix-stripping helper that
// callers use to feed the matcher engine. Substring and regex
// inputs pass through unchanged; fuzzy / literal lose exactly
// one leading sigil.
func TestTrimSearchPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		wantMode  SearchMode
		wantInput string
	}{
		{"empty stays empty substring", "", SearchSubstring, ""},
		{"substring passes through", "diskfull", SearchSubstring, "diskfull"},
		{"regex passes through", "web.*api", SearchRegex, "web.*api"},
		{"fuzzy strips leading tilde", "~dskfll", SearchFuzzy, "dskfll"},
		{"literal strips leading backslash", `\web.api`, SearchLiteral, "web.api"},
		{"fuzzy strips only one tilde", "~~tilde~", SearchFuzzy, "~tilde~"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotMode, gotInput := TrimSearchPrefix(tc.input)
			require.Equal(t, tc.wantMode, gotMode)
			require.Equal(t, tc.wantInput, gotInput)
		})
	}
}

// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
)

func TestFormatMatchersRoundTrip(t *testing.T) {
	t.Parallel()
	// Includes values that themselves contain an operator-like
	// substring (`a!=b`, `a=b`, `=~regex`) so the leftmost-position
	// parser is exercised — alerts in the wild can carry such
	// values in annotations, and a lossy round-trip would silently
	// rewrite the matcher when the user opens an `e` form.
	in := []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "severity", Value: "warning|critical", IsRegex: true, IsEqual: true},
		{Name: "team", Value: "platform"},
		{Name: "instance", Value: ".*-canary", IsRegex: true},
		{Name: "expr", Value: "a!=b", IsEqual: true},
		{Name: "expr2", Value: "a=b", IsEqual: true},
		{Name: "expr3", Value: "x=~y", IsEqual: false},
	}
	rendered := formatMatchers(in)
	parsed, err := matcher.Parse(rendered)
	require.NoError(t, err)
	require.Equal(t, in, parsed)
}

// TestParseEndsAt_DurationShorthand covers the Duration shorthand
// wire-up: the form's Ends field accepts the wider grammar (7d,
// 1w2d3h, 1.5h, ...) via timerender.Parse with a tailored error
// for capital `M` and a unified "not a duration" message for
// non-shorthand garbage (so the old RFC3339 internal-format error
// never surfaces to the operator). RFC3339 timestamps still parse
// when the input contains no letter that could be a unit attempt.
func TestParseEndsAt_DurationShorthand(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		in       string
		wantTime time.Time
		wantErr  string
	}{
		{name: "7d shorthand resolves to base+7d", in: "7d", wantTime: base.Add(7 * 24 * time.Hour)},
		{name: "mixed-unit shorthand", in: "2h30m", wantTime: base.Add(2*time.Hour + 30*time.Minute)},
		{name: "rfc3339 timestamp still parses", in: "2026-04-25T14:00:00Z", wantTime: time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)},

		{name: "empty surfaces blank-ends sentinel", in: "", wantErr: "ends is required"},
		{name: "capital M tailored", in: "1M", wantErr: "M is not a unit; m means minute (1m=60s); use 30d if you meant ~month"},
		{name: "capital W tailored", in: "1W", wantErr: "W is not a unit; w means week (1w=7d)"},
		{name: "out of order surfaces rewrite hint", in: "2h7d", wantErr: "units must be ordered largest-first; rewrite as 7d2h"},

		{name: "garbage with a unit-ish letter surfaces duration error", in: "hello", wantErr: "not a duration (try 7d, 2h30m, 1w2d)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseEndsAt(tc.in, base)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.True(t, got.Equal(tc.wantTime), "want %s, got %s", tc.wantTime, got)
		})
	}
}

// TestParseEndsAt_NoLetterFallsBackToRFC3339Error pins the
// disambiguation rule: when the input contains no letter that
// could be a unit attempt (digits, dashes, colons), the failing
// duration parse defers to RFC3339 so the operator sees the
// timestamp parser's error rather than the misleading "not a
// duration" message. Asserting on `*time.ParseError` (rather than
// the wrapped message text) is tight against the contract — the
// stdlib message string is implementation detail and could shift
// across Go versions; the type is the guarantee.
func TestParseEndsAt_NoLetterFallsBackToRFC3339Error(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	_, err := parseEndsAt("2026-04-25", base)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "not a duration",
		"a digit-and-dash input must surface the RFC3339 parse error, not the duration grammar message")
	var perr *time.ParseError
	require.ErrorAs(t, err, &perr,
		"the RFC3339 fallback should surface stdlib's typed *time.ParseError")
}

func TestMatchersFromLabels_DropsNameAndSorts(t *testing.T) {
	t.Parallel()
	got := MatchersFromLabels(map[string]string{
		"__name__":  "ALERTS",
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "host-1",
	})
	require.Equal(t, []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "instance", Value: "host-1", IsEqual: true},
		{Name: "severity", Value: "critical", IsEqual: true},
	}, got, "synthetic __name__ must be dropped; output stable-sorted by name")
}

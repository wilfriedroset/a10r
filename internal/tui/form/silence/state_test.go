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
		{name: "zone-less timestamp read as local", in: "2026-06-01 10:00:00", wantTime: time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local)}, //nolint:gosmopolitan // asserts the local-time contract

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

// TestParseAbsTime covers the absolute-timestamp grammar shared by
// the Starts and Ends fields: full RFC3339 (with Z or an offset)
// keeps its instant, while the zone-less shapes an operator reads
// back off the display (timerender.absoluteFormat, ISO local) are
// interpreted in time.Local. Anything else returns ok=false so the
// caller can surface a friendly hint instead of leaking stdlib's
// layout string.
func TestParseAbsTime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want time.Time
		ok   bool
	}{
		{name: "rfc3339 Z keeps utc instant", in: "2026-06-01T10:00:00Z", want: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), ok: true},
		{name: "rfc3339 offset keeps instant", in: "2026-06-01T10:00:00+02:00", want: time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC), ok: true},
		{name: "zone-less T read as local", in: "2026-06-01T10:00:00", want: time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local), ok: true},          //nolint:gosmopolitan // asserts the local-time contract
		{name: "display format space read as local", in: "2026-06-01 10:00:00", want: time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local), ok: true}, //nolint:gosmopolitan // asserts the local-time contract
		{name: "minute precision read as local", in: "2026-06-01 10:00", want: time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local), ok: true},        //nolint:gosmopolitan // asserts the local-time contract
		{name: "date only is local midnight", in: "2026-06-01", want: time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local), ok: true},                  //nolint:gosmopolitan // asserts the local-time contract

		{name: "z and offset together rejected", in: "2026-06-01T10:00:00Z02:00", ok: false},
		{name: "garbage rejected", in: "soon", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseAbsTime(tc.in)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				require.True(t, got.Equal(tc.want), "want %s, got %s", tc.want, got)
			}
		})
	}
}

// TestParseEndsAt_InvalidTimestampFriendlyError pins the no-letter
// fallback: a timestamp-shaped input that parses as neither a
// duration nor any accepted layout surfaces the friendly hint, not
// stdlib's `*time.ParseError` layout string and not the "not a
// duration" message (the input carried no unit letter, so the
// operator was reaching for a timestamp).
func TestParseEndsAt_InvalidTimestampFriendlyError(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	_, err := parseEndsAt("2026-06-01T10:00:00Z02:00", base)
	require.EqualError(t, err,
		"not a valid time (try 2h, or a timestamp like 2026-06-01 10:00:00, optionally Z or +02:00)")
	var perr *time.ParseError
	require.NotErrorAs(t, err, &perr, "stdlib's layout error must not leak to the operator")
}

// TestParseTimeOrNow covers the Starts field: empty / "now" yield
// the supplied now, accepted timestamps parse (zone-less as local),
// and garbage surfaces the friendly hint without the duration cue
// the Starts field has no use for.
func TestParseTimeOrNow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

	for _, in := range []string{"", "now", "  now  "} {
		got, err := parseTimeOrNow(in, now)
		require.NoError(t, err)
		require.True(t, got.Equal(now), "%q should resolve to now", in)
	}

	got, err := parseTimeOrNow("2026-06-01 10:00:00", now)
	require.NoError(t, err)
	require.True(t, got.Equal(time.Date(2026, 6, 1, 10, 0, 0, 0, time.Local))) //nolint:gosmopolitan // asserts the local-time contract

	_, err = parseTimeOrNow("whenever", now)
	require.EqualError(t, err,
		"not a valid time (use now or a timestamp like 2026-06-01 10:00:00, optionally Z or +02:00)")
}

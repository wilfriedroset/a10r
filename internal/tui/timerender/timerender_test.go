// SPDX-License-Identifier: Apache-2.0

package timerender

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormat_String(t *testing.T) {
	t.Parallel()
	require.Equal(t, "relative", Relative.String())
	require.Equal(t, "absolute", Absolute.String())
}

func TestDuration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "zero", d: 0, want: "0s"},
		{name: "sub-second", d: 500 * time.Millisecond, want: "0s"},
		{name: "1s", d: time.Second, want: "1s"},
		{name: "59s", d: 59 * time.Second, want: "59s"},
		{name: "1m boundary", d: time.Minute, want: "1m"},
		{name: "2m", d: 2 * time.Minute, want: "2m"},
		{name: "1h boundary", d: time.Hour, want: "1h"},
		{name: "3h", d: 3 * time.Hour, want: "3h"},
		{name: "1d boundary", d: 24 * time.Hour, want: "1d"},
		{name: "5d", d: 5 * 24 * time.Hour, want: "5d"},
		{name: "10y uptime", d: 10 * 365 * 24 * time.Hour, want: "3650d"},
		{name: "negative collapses to abs", d: -2 * time.Hour, want: "2h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, Duration(tc.d))
		})
	}
}

func TestDisplay_Relative(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		ts   time.Time
		want string
	}{
		{name: "zero ts is empty", ts: time.Time{}, want: ""},
		{name: "exactly now is now", ts: now, want: "now"},
		{name: "sub-second past is now", ts: now.Add(-500 * time.Millisecond), want: "now"},
		{name: "sub-second future is now", ts: now.Add(500 * time.Millisecond), want: "now"},

		{name: "1s ago boundary", ts: now.Add(-time.Second), want: "1s ago"},
		{name: "5s ago", ts: now.Add(-5 * time.Second), want: "5s ago"},
		{name: "59s ago", ts: now.Add(-59 * time.Second), want: "59s ago"},
		{name: "60s ago is 1m ago boundary", ts: now.Add(-60 * time.Second), want: "1m ago"},
		{name: "2m ago", ts: now.Add(-2 * time.Minute), want: "2m ago"},
		{name: "60m ago is 1h ago boundary", ts: now.Add(-60 * time.Minute), want: "1h ago"},
		{name: "3h ago", ts: now.Add(-3 * time.Hour), want: "3h ago"},
		{name: "24h ago is 1d ago boundary", ts: now.Add(-24 * time.Hour), want: "1d ago"},
		{name: "5d ago", ts: now.Add(-5 * 24 * time.Hour), want: "5d ago"},
		{name: "30d ago", ts: now.Add(-30 * 24 * time.Hour), want: "30d ago"},

		{name: "in 1s boundary", ts: now.Add(time.Second), want: "in 1s"},
		{name: "in 5s", ts: now.Add(5 * time.Second), want: "in 5s"},
		{name: "in 60s is in 1m boundary", ts: now.Add(60 * time.Second), want: "in 1m"},
		{name: "in 30m", ts: now.Add(30 * time.Minute), want: "in 30m"},
		{name: "in 60m is in 1h boundary", ts: now.Add(60 * time.Minute), want: "in 1h"},
		{name: "in 2h", ts: now.Add(2 * time.Hour), want: "in 2h"},
		{name: "in 24h is in 1d boundary", ts: now.Add(24 * time.Hour), want: "in 1d"},
		{name: "in 30d", ts: now.Add(30 * 24 * time.Hour), want: "in 30d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, Display(Relative, now, tc.ts))
		})
	}
}

func TestDisplay_Absolute(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	require.Empty(t, Display(Absolute, now, time.Time{}),
		"zero ts must render empty regardless of format")

	ts := time.Date(2026, 5, 1, 13, 45, 0, 0, time.UTC)
	got := Display(Absolute, now, ts)
	//nolint:gosmopolitan // mirrors Display's deliberate local-zone rendering
	require.Equal(t, ts.Local().Format(absoluteFormat), got,
		"absolute format must use the local-zone ISO layout")
}

func TestRemaining(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		when time.Duration
		want string
	}{
		{"past returns empty", -time.Hour, ""},
		{"zero returns empty", 0, ""},
		{"sub-minute renders seconds", 30 * time.Second, "30s"},
		{"sub-hour renders minutes", 45 * time.Minute, "45m"},
		{"hours and minutes", 2*time.Hour + 13*time.Minute, "2h13m"},
		{"whole hours drop the m suffix", 3 * time.Hour, "3h"},
		{"days swallow hours and minutes", 49 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Remaining(now, now.Add(tc.when))
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr string
	}{
		{name: "7d", in: "7d", want: 7 * 24 * time.Hour},
		{name: "2w", in: "2w", want: 14 * 24 * time.Hour},
		{name: "12h", in: "12h", want: 12 * time.Hour},
		{name: "45m", in: "45m", want: 45 * time.Minute},
		{name: "2h", in: "2h", want: 2 * time.Hour},
		{name: "2h30m", in: "2h30m", want: 2*time.Hour + 30*time.Minute},
		{name: "1w2d3h", in: "1w2d3h", want: 9*24*time.Hour + 3*time.Hour},
		{name: "1.5h", in: "1.5h", want: 90 * time.Minute},
		{name: "0.5d", in: "0.5d", want: 12 * time.Hour},
		{name: "spaces between terms", in: "1w 2d", want: 9 * 24 * time.Hour},
		{name: "30s", in: "30s", want: 30 * time.Second},
		{name: "all five units largest-first", in: "1w1d1h1m1s", want: 8*24*time.Hour + time.Hour + time.Minute + time.Second},
		{name: "leading and trailing whitespace tolerated", in: "  2h  ", want: 2 * time.Hour},
		{name: "float rounds to nearest second", in: "0.3333h", want: 1200 * time.Second},
		{name: "sub-second rounds to zero", in: "0.0001s", want: 0},

		{name: "empty", in: "", wantErr: "empty duration"},
		{name: "whitespace-only", in: "   ", wantErr: "empty duration"},
		{name: "capital M", in: "1M", wantErr: "M is not a unit; m means minute (1m=60s); use 30d if you meant ~month"},
		{name: "capital W", in: "1W", wantErr: "W is not a unit; w means week (1w=7d)"},
		{name: "capital Y", in: "1Y", wantErr: "Y is not a unit; years are not supported; use 365d"},
		{name: "unknown unit days", in: "7days", wantErr: `unknown unit "days" (use s m h d w)`},
		{name: "unknown unit x", in: "7x", wantErr: `unknown unit "x" (use s m h d w)`},
		{name: "unknown unit xyz", in: "7xyz", wantErr: `unknown unit "xyz" (use s m h d w)`},
		{name: "out of order 2h7d", in: "2h7d", wantErr: "units must be ordered largest-first; rewrite as 7d2h"},
		{name: "out of order 5m30s2h", in: "5m30s2h", wantErr: "units must be ordered largest-first; rewrite as 2h5m30s"},
		{name: "repeated unit", in: "1d1d", wantErr: `unit "d" appears more than once`},
		{name: "no unit number only", in: "7", wantErr: "missing unit; use s m h d w"},
		{name: "no number before unit dx", in: "dx", wantErr: `expected number before unit "d"`},
		{name: "no number before unit xh", in: "xh", wantErr: `expected number before unit "x"`},
		{name: "no number leading h", in: "h", wantErr: `expected number before unit "h"`},
		{name: "garbage hello", in: "hello", wantErr: "not a duration (try 7d, 2h30m, 1w2d)"},
		{name: "garbage symbols", in: "@@@", wantErr: "not a duration (try 7d, 2h30m, 1w2d)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tc.in)
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestNextAttempt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		deadline time.Time
		want     string
	}{
		{name: "zero deadline is past-due", deadline: time.Time{}, want: "retrying now"},
		{name: "negative delta is past-due", deadline: now.Add(-time.Second), want: "retrying now"},
		{name: "sub-second future is past-due", deadline: now.Add(999 * time.Millisecond), want: "retrying now"},
		{name: "exactly 1s renders seconds", deadline: now.Add(time.Second), want: "retrying in 1s"},
		{name: "59s renders seconds", deadline: now.Add(59 * time.Second), want: "retrying in 59s"},
		{name: "60s rolls into minutes", deadline: now.Add(60 * time.Second), want: "retrying in 1m"},
		{name: "59m renders minutes", deadline: now.Add(59 * time.Minute), want: "retrying in 59m"},
		{name: "60m rolls into hours", deadline: now.Add(60 * time.Minute), want: "retrying in 1h"},
		{name: "above a day rolls into the d rung", deadline: now.Add(25 * time.Hour), want: "retrying in 1d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, NextAttempt(now, tc.deadline))
		})
	}
}

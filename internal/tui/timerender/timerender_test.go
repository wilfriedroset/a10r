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

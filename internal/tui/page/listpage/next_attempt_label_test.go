// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNextAttemptLabel pins the s/m/h/d ladder (inherited from
// timerender.Duration) and the past-due boundary so a future
// ladder edit can't silently reshape the suffix. ErrorBand calls
// this through the renderer, so a regression here would still
// surface in the table tests — but the per-unit boundary cases
// are easier to read at the helper level.
func TestNextAttemptLabel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		nextAt time.Time
		want   string
	}{
		{name: "zero NextAt is past-due", nextAt: time.Time{}, want: "retrying now"},
		{name: "negative delta is past-due", nextAt: now.Add(-time.Second), want: "retrying now"},
		{name: "sub-second future is past-due", nextAt: now.Add(999 * time.Millisecond), want: "retrying now"},
		{name: "exactly 1s renders seconds", nextAt: now.Add(time.Second), want: "retrying in 1s"},
		{name: "59s renders seconds", nextAt: now.Add(59 * time.Second), want: "retrying in 59s"},
		{name: "60s rolls into minutes", nextAt: now.Add(60 * time.Second), want: "retrying in 1m"},
		{name: "59m renders minutes", nextAt: now.Add(59 * time.Minute), want: "retrying in 59m"},
		{name: "60m rolls into hours", nextAt: now.Add(60 * time.Minute), want: "retrying in 1h"},
		{name: "above a day rolls into the d rung (shared FormatDuration ladder)", nextAt: now.Add(25 * time.Hour), want: "retrying in 1d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, nextAttemptLabel(now, tc.nextAt))
		})
	}
}

// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestRefreshCountdown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	future := now.Add(25 * time.Second)
	past := now.Add(-1 * time.Second)

	cases := []struct {
		name       string
		paused     bool
		refreshing bool
		polled     bool
		soonest    time.Time
		want       string
	}{
		{
			name:   "paused",
			paused: true, refreshing: false,
			want: "WATCH OFF",
		},
		{
			name:   "paused with refresh in flight",
			paused: true, refreshing: true,
			want: "WATCH OFF · refreshing…",
		},
		{
			name:       "refreshing alone",
			refreshing: true,
			want:       "refreshing…",
		},
		{
			name:   "pre-poll (not paused, not refreshing, not polled)",
			polled: false,
			want:   "",
		},
		{
			name:   "polled but no NextAt published",
			polled: true, soonest: time.Time{},
			want: "",
		},
		{
			name:   "polled with future NextAt",
			polled: true, soonest: future,
			want: "next refresh 25s",
		},
		{
			name:   "polled with past-due NextAt reads as due",
			polled: true, soonest: past,
			want: "next refresh due",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := listpage.RefreshCountdown(tc.paused, tc.refreshing, tc.polled, tc.soonest, now)
			require.Equal(t, tc.want, got)
		})
	}
}

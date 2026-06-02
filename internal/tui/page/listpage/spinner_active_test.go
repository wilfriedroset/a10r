// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestSpinnerActive(t *testing.T) {
	t.Parallel()

	always := func(string) bool { return true }
	never := func(string) bool { return false }

	cases := []struct {
		name       string
		polled     map[string]struct{}
		refreshing bool
		includes   func(string) bool
		want       bool
	}{
		{
			name:       "cold start, includes-all",
			polled:     nil,
			refreshing: false,
			includes:   always,
			want:       true,
		},
		{
			name:       "polled, idle",
			polled:     map[string]struct{}{"a": {}},
			refreshing: false,
			includes:   always,
			want:       false,
		},
		{
			name:       "polled, refreshing",
			polled:     map[string]struct{}{"a": {}},
			refreshing: true,
			includes:   always,
			want:       true,
		},
		{
			name:       "polled but tenant out of scope",
			polled:     map[string]struct{}{"a": {}},
			refreshing: false,
			includes:   never,
			want:       true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := &listpage.PollingUI{PolledTenants: tc.polled, Refreshing: tc.refreshing}
			require.Equal(t, tc.want, u.SpinnerActive(tc.includes))
		})
	}
}

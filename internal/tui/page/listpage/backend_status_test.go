// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

func TestBase_HandleBackendStatusMsg(t *testing.T) {
	t.Parallel()

	nextAt := time.Date(2026, 5, 18, 12, 0, 5, 0, time.UTC)

	cases := []struct {
		name    string
		tenants []string
		start   map[string]listpage.BackendHealth
		msg     poll.BackendStatusMsg
		want    map[string]listpage.BackendHealth
	}{
		{
			name:    "failure on known tenant writes entry",
			tenants: []string{"prod", "staging"},
			start:   map[string]listpage.BackendHealth{},
			msg: poll.BackendStatusMsg{
				Tenant:   "prod",
				State:    header.ConnUnreachable,
				Detail:   "connection refused",
				Failures: 2,
				NextAt:   nextAt,
			},
			want: map[string]listpage.BackendHealth{
				"prod": {
					State:    header.ConnUnreachable,
					Detail:   "connection refused",
					Failures: 2,
					NextAt:   nextAt,
				},
			},
		},
		{
			name:    "subsequent failure on same tenant replaces entry",
			tenants: []string{"prod"},
			start: map[string]listpage.BackendHealth{
				"prod": {State: header.ConnUnreachable, Detail: "old", Failures: 1},
			},
			msg: poll.BackendStatusMsg{
				Tenant:   "prod",
				State:    header.ConnDegraded,
				Detail:   "401 unauthorised",
				Failures: 3,
				NextAt:   nextAt,
			},
			want: map[string]listpage.BackendHealth{
				"prod": {
					State:    header.ConnDegraded,
					Detail:   "401 unauthorised",
					Failures: 3,
					NextAt:   nextAt,
				},
			},
		},
		{
			name:    "recovery clears the entry",
			tenants: []string{"prod"},
			start: map[string]listpage.BackendHealth{
				"prod": {State: header.ConnUnreachable, Detail: "down", Failures: 5},
			},
			msg: poll.BackendStatusMsg{
				Tenant: "prod",
				State:  header.ConnConnected,
			},
			want: map[string]listpage.BackendHealth{},
		},
		{
			name:    "unknown tenant is dropped",
			tenants: []string{"prod"},
			start:   map[string]listpage.BackendHealth{},
			msg: poll.BackendStatusMsg{
				Tenant: "ghost",
				State:  header.ConnUnreachable,
				Detail: "stray",
			},
			want: map[string]listpage.BackendHealth{},
		},
		{
			name:    "empty Tenants disables the known-tenant guard",
			tenants: nil,
			start:   map[string]listpage.BackendHealth{},
			msg: poll.BackendStatusMsg{
				Tenant: "anything",
				State:  header.ConnUnreachable,
				Detail: "no list configured",
			},
			want: map[string]listpage.BackendHealth{
				"anything": {
					State:  header.ConnUnreachable,
					Detail: "no list configured",
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Tenants:       tc.tenants,
				BackendHealth: tc.start,
			}
			b.HandleBackendStatusMsg(tc.msg)
			require.Equal(t, tc.want, b.BackendHealth)
		})
	}
}

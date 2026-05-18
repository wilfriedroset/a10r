// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// payload is a placeholder resource type so the helper's
// genericity is exercised without dragging the backend package
// into listpage's test scope.
type payload struct {
	N int
}

func TestApplyDataMsg_PanicsWithoutRecompute(t *testing.T) {
	t.Parallel()

	b := &listpage.Base{Tenants: []string{"prod"}}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{},
	}

	require.PanicsWithValue(t,
		"listpage.ApplyDataMsg: Base.Recompute callback not wired by page constructor",
		func() {
			listpage.ApplyDataMsg(b, u, poll.DataMsg{
				Resource: payload{},
				Tenant:   "prod",
			}, func(string, payload) {})
		},
	)
}

func TestApplyDataMsg_ZeroNextAtPreservesPriorEntry(t *testing.T) {
	t.Parallel()

	prior := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	b := &listpage.Base{
		Tenants:   []string{"prod"},
		Recompute: func() {},
	}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{"prod": prior},
	}

	handled := listpage.ApplyDataMsg(b, u, poll.DataMsg{
		Resource: payload{},
		Tenant:   "prod",
		// NextAt deliberately zero — covers legacy/test DataMsgs.
	}, func(string, payload) {})

	require.True(t, handled)
	require.Equal(t, prior, u.NextRefresh["prod"], "zero NextAt must not clobber a prior entry")
}

func TestApplyDataMsg_RefreshingClearedOnlyForInScopeReply(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		scope          string
		tenant         string
		wantRefreshing bool
	}{
		{name: "in-scope clears refreshing", scope: "prod", tenant: "prod", wantRefreshing: false},
		{name: "out-of-scope leaves refreshing high", scope: "prod", tenant: "staging", wantRefreshing: true},
		{name: "all-scope clears refreshing", scope: "all", tenant: "staging", wantRefreshing: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{
				Scope:     tc.scope,
				Tenants:   []string{"prod", "staging"},
				Recompute: func() {},
			}
			u := &listpage.PollingUI{
				PolledTenants: map[string]struct{}{},
				NextRefresh:   map[string]time.Time{},
				Refreshing:    true,
			}

			handled := listpage.ApplyDataMsg(b, u, poll.DataMsg{
				Resource: payload{},
				Tenant:   tc.tenant,
			}, func(string, payload) {})

			require.True(t, handled)
			require.Equal(t, tc.wantRefreshing, u.Refreshing)
		})
	}
}

func TestApplyDataMsg_PausedDropsSnapshot(t *testing.T) {
	t.Parallel()

	stores := 0
	b := &listpage.Base{
		Tenants:   []string{"prod"},
		Paused:    true,
		Recompute: func() {},
	}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{},
	}

	handled := listpage.ApplyDataMsg(b, u, poll.DataMsg{
		Resource: payload{N: 1},
		Tenant:   "prod",
	}, func(string, payload) { stores++ })

	require.True(t, handled, "paused page claims and drops the DataMsg")
	require.Zero(t, stores, "paused page must not write byTenant under the cursor")
}

func TestApplyDataMsg_PausedRefreshLetsOneSnapshotThrough(t *testing.T) {
	t.Parallel()

	stores := 0
	b := &listpage.Base{
		Tenants:   []string{"prod"},
		Paused:    true,
		Recompute: func() {},
	}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{},
		PausedRefresh: true,
	}

	handled := listpage.ApplyDataMsg(b, u, poll.DataMsg{
		Resource: payload{N: 7},
		Tenant:   "prod",
	}, func(string, payload) { stores++ })

	require.True(t, handled)
	require.Equal(t, 1, stores, "PausedRefresh must let one DataMsg through")
	require.False(t, u.PausedRefresh, "PausedRefresh must self-clear after the next DataMsg")
}

func TestApplyDataMsg_UnknownTenantClaimedAndIgnored(t *testing.T) {
	t.Parallel()

	recomputes := 0
	stores := 0
	b := &listpage.Base{
		Tenants:   []string{"prod"},
		Recompute: func() { recomputes++ },
	}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{},
	}

	msg := poll.DataMsg{
		Resource: payload{N: 1},
		Tenant:   "intruder",
	}

	handled := listpage.ApplyDataMsg(b, u, msg, func(string, payload) { stores++ })

	require.True(t, handled, "unknown-tenant DataMsg is claimed so the page short-circuits")
	require.Zero(t, stores, "store must not be called for an unknown tenant")
	require.Zero(t, recomputes, "Recompute must not fire for an unknown tenant")
	require.NotContains(t, u.PolledTenants, "intruder", "PolledTenants must reject the unknown tenant")
}

func TestApplyDataMsg_WrongPayloadFallsThrough(t *testing.T) {
	t.Parallel()

	recomputes := 0
	stores := 0
	b := &listpage.Base{
		Tenants:   []string{"prod"},
		Recompute: func() { recomputes++ },
	}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{},
	}

	msg := poll.DataMsg{
		Resource: "not the expected payload type",
		Tenant:   "prod",
	}

	handled := listpage.ApplyDataMsg(b, u, msg, func(string, payload) { stores++ })

	require.False(t, handled, "wrong payload type must fall through so the page's main switch can see it")
	require.Zero(t, stores, "store must not be called for a wrong payload type")
	require.Zero(t, recomputes, "Recompute must not fire for a wrong payload type")
}

func TestApplyDataMsg_HappyPath(t *testing.T) {
	t.Parallel()

	recomputes := 0
	stored := map[string]payload{}
	now := time.Now()
	nextAt := now.Add(10 * time.Second)
	b := &listpage.Base{
		Scope:     "all",
		Tenants:   []string{"prod"},
		Recompute: func() { recomputes++ },
	}
	u := &listpage.PollingUI{
		PolledTenants: map[string]struct{}{},
		NextRefresh:   map[string]time.Time{},
	}

	msg := poll.DataMsg{
		Resource: payload{N: 42},
		Tenant:   "prod",
		NextAt:   nextAt,
	}

	handled := listpage.ApplyDataMsg(b, u, msg, func(tenant string, p payload) {
		stored[tenant] = p
	})

	require.True(t, handled, "happy-path DataMsg must be claimed by the helper")
	require.Equal(t, payload{N: 42}, stored["prod"], "store callback must receive the typed payload")
	require.Equal(t, 1, recomputes, "Recompute must fire exactly once")
	require.Equal(t, nextAt, u.NextRefresh["prod"], "NextRefresh must capture msg.NextAt")
	require.Contains(t, u.PolledTenants, "prod", "PolledTenants must record the tenant")
}

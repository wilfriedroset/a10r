// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_HandleScopeChangedMsg(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		startScope string
		msg        app.ScopeChangedMsg
		wantScope  string
	}{
		{name: "all to single tenant", startScope: "all", msg: app.ScopeChangedMsg{Scope: "prod"}, wantScope: "prod"},
		{name: "single to comma-joined", startScope: "prod", msg: app.ScopeChangedMsg{Scope: "prod,staging"}, wantScope: "prod,staging"},
		{name: "to empty scope", startScope: "prod", msg: app.ScopeChangedMsg{Scope: ""}, wantScope: ""},
		{name: "to identical scope", startScope: "all", msg: app.ScopeChangedMsg{Scope: "all"}, wantScope: "all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			b := &listpage.Base{
				Scope:     tc.startScope,
				Recompute: func() { calls++ },
			}
			b.HandleScopeChangedMsg(tc.msg)
			require.Equal(t, tc.wantScope, b.Scope)
			require.Equal(t, 1, calls, "recompute must fire exactly once")
		})
	}
}

func TestBase_HandleScopeChangedMsg_PanicsWithoutRecompute(t *testing.T) {
	t.Parallel()
	b := &listpage.Base{}
	require.PanicsWithValue(t,
		"listpage.Base.HandleScopeChangedMsg: Recompute callback not wired by page constructor",
		func() { b.HandleScopeChangedMsg(app.ScopeChangedMsg{Scope: "prod"}) },
	)
}

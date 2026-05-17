// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_ShowTenantColumn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		scope    string
		tenants  []string
		observed int
		want     bool
	}{
		{name: "non-all scope hides column", scope: "prod", tenants: []string{"prod", "staging"}, observed: 2, want: false},
		{name: "comma-joined scope hides column", scope: "prod,staging", tenants: []string{"prod", "staging"}, observed: 2, want: false},
		{name: "all scope with two tenants shows column", scope: "all", tenants: []string{"prod", "staging"}, observed: 0, want: true},
		{name: "all scope with one tenant hides column", scope: "all", tenants: []string{"prod"}, observed: 0, want: false},
		{name: "all scope with empty tenants falls back to observed > 1", scope: "all", tenants: nil, observed: 2, want: true},
		{name: "all scope with empty tenants and observed=1 hides column", scope: "all", tenants: nil, observed: 1, want: false},
		{name: "all scope with empty tenants and observed=0 hides column", scope: "all", tenants: nil, observed: 0, want: false},
		{name: "configured fleet wins over observed", scope: "all", tenants: []string{"prod", "broken"}, observed: 1, want: true},
		{name: "empty scope is not scopeAll so hides column", scope: "", tenants: []string{"prod", "staging"}, observed: 2, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{Scope: tc.scope, Tenants: tc.tenants}
			require.Equal(t, tc.want, b.ShowTenantColumn(tc.observed))
		})
	}
}

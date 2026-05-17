// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_ScopeIncludes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		scope  string
		tenant string
		want   bool
	}{
		{name: "empty scope includes any tenant", scope: "", tenant: "prod", want: true},
		{name: "all scope includes any tenant", scope: "all", tenant: "prod", want: true},
		{name: "single tenant matches itself", scope: "prod", tenant: "prod", want: true},
		{name: "single tenant rejects other", scope: "prod", tenant: "staging", want: false},
		{name: "comma list matches first", scope: "prod,staging", tenant: "prod", want: true},
		{name: "comma list matches last", scope: "prod,staging", tenant: "staging", want: true},
		{name: "comma list rejects outsider", scope: "prod,staging", tenant: "dev", want: false},
		{name: "leading whitespace trimmed", scope: "  prod  ,staging", tenant: "prod", want: true},
		{name: "trailing whitespace trimmed", scope: "prod,  staging  ", tenant: "staging", want: true},
		{name: "outer whitespace around all trims to all", scope: "  all  ", tenant: "anything", want: true},
		{name: "case-sensitive miss", scope: "Prod", tenant: "prod", want: false},
		{name: "empty tenant rejected by non-empty scope", scope: "prod", tenant: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{Scope: tc.scope}
			require.Equal(t, tc.want, b.ScopeIncludes(tc.tenant))
		})
	}
}

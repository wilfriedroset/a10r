// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestBase_KnownTenant(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tenants []string
		tenant  string
		want    bool
	}{
		{name: "empty list disables guard", tenants: nil, tenant: "anything", want: true},
		{name: "single configured tenant matches", tenants: []string{"prod"}, tenant: "prod", want: true},
		{name: "single configured tenant rejects other", tenants: []string{"prod"}, tenant: "staging", want: false},
		{name: "multi-tenant matches first", tenants: []string{"prod", "staging"}, tenant: "prod", want: true},
		{name: "multi-tenant rejects unknown", tenants: []string{"prod", "staging"}, tenant: "dev", want: false},
		{name: "empty tenant rejected by non-empty list", tenants: []string{"prod"}, tenant: "", want: false},
		{name: "case-sensitive miss", tenants: []string{"Prod"}, tenant: "prod", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &listpage.Base{Tenants: tc.tenants}
			require.Equal(t, tc.want, b.KnownTenant(tc.tenant))
		})
	}
}

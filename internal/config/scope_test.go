// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScopeMatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		scope  string
		tenant string
		want   bool
	}{
		{name: "empty covers all", scope: "", tenant: "prod", want: true},
		{name: "all covers all", scope: "all", tenant: "prod", want: true},
		{name: "all trims", scope: "  all  ", tenant: "prod", want: true},
		{name: "single match", scope: "prod", tenant: "prod", want: true},
		{name: "single miss", scope: "prod", tenant: "staging", want: false},
		{name: "list match first", scope: "prod,staging", tenant: "prod", want: true},
		{name: "list match second", scope: "prod,staging", tenant: "staging", want: true},
		{name: "list miss", scope: "prod,staging", tenant: "dev", want: false},
		{name: "list trims members", scope: "prod , staging", tenant: "staging", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ScopeMatches(tc.scope, tc.tenant))
		})
	}
}

func TestUnknownScopeTenants(t *testing.T) {
	t.Parallel()

	all := []Backend{{Name: "prod"}, {Name: "staging"}}
	cases := []struct {
		name  string
		scope string
		want  []string
	}{
		{name: "empty has none", scope: "", want: nil},
		{name: "all has none", scope: "all", want: nil},
		{name: "single known", scope: "prod", want: nil},
		{name: "single unknown", scope: "bogus", want: []string{"bogus"}},
		{name: "list one unknown", scope: "prod,bogus", want: []string{"bogus"}},
		{name: "list several unknown", scope: "bogus,prod,typo", want: []string{"bogus", "typo"}},
		{name: "trailing comma ignored", scope: "prod,", want: nil},
		{name: "empty middle ignored", scope: "prod, ,staging", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, UnknownScopeTenants(all, tc.scope))
		})
	}
}

func TestScopeBackends(t *testing.T) {
	t.Parallel()

	all := []Backend{{Name: "prod"}, {Name: "staging"}, {Name: "dev"}}

	cases := []struct {
		name      string
		scope     string
		wantNames []string
	}{
		{name: "empty keeps all", scope: "", wantNames: []string{"prod", "staging", "dev"}},
		{name: "all keeps all", scope: "all", wantNames: []string{"prod", "staging", "dev"}},
		{name: "single narrows", scope: "staging", wantNames: []string{"staging"}},
		{name: "list narrows", scope: "prod,dev", wantNames: []string{"prod", "dev"}},
		{name: "no match yields empty", scope: "nope", wantNames: []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ScopeBackends(all, tc.scope)
			names := make([]string, 0, len(got))
			for _, b := range got {
				names = append(names, b.Name)
			}
			require.Equal(t, tc.wantNames, names)
		})
	}
}

// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestPolledInScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		polled  map[string]struct{}
		inScope map[string]bool
		want    bool
	}{
		{name: "empty polled set", polled: nil, want: false},
		{
			name:    "single tenant in scope",
			polled:  map[string]struct{}{"a": {}},
			inScope: map[string]bool{"a": true},
			want:    true,
		},
		{
			name:    "single tenant out of scope",
			polled:  map[string]struct{}{"a": {}},
			inScope: map[string]bool{"a": false},
			want:    false,
		},
		{
			name:    "one of two in scope",
			polled:  map[string]struct{}{"a": {}, "b": {}},
			inScope: map[string]bool{"a": false, "b": true},
			want:    true,
		},
		{
			name:    "all out of scope",
			polled:  map[string]struct{}{"a": {}, "b": {}},
			inScope: map[string]bool{"a": false, "b": false},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := &listpage.PollingUI{PolledTenants: tc.polled}
			includes := func(s string) bool { return tc.inScope[s] }
			require.Equal(t, tc.want, u.PolledInScope(includes))
		})
	}
}

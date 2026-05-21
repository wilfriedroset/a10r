// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestSoonestNextRefresh(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	earlier := t0.Add(5 * time.Second)
	later := t0.Add(30 * time.Second)

	cases := []struct {
		name    string
		next    map[string]time.Time
		inScope map[string]bool
		want    time.Time
	}{
		{name: "empty map", next: nil, want: time.Time{}},
		{
			name:    "single in scope",
			next:    map[string]time.Time{"a": earlier},
			inScope: map[string]bool{"a": true},
			want:    earlier,
		},
		{
			name:    "single out of scope",
			next:    map[string]time.Time{"a": earlier},
			inScope: map[string]bool{"a": false},
			want:    time.Time{},
		},
		{
			name:    "two in scope: pick earliest",
			next:    map[string]time.Time{"a": later, "b": earlier},
			inScope: map[string]bool{"a": true, "b": true},
			want:    earlier,
		},
		{
			name:    "two but only later is in scope",
			next:    map[string]time.Time{"a": earlier, "b": later},
			inScope: map[string]bool{"a": false, "b": true},
			want:    later,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := &listpage.PollingUI{NextRefresh: tc.next}
			includes := func(s string) bool { return tc.inScope[s] }
			require.Equal(t, tc.want, u.SoonestNextRefresh(includes))
		})
	}
}

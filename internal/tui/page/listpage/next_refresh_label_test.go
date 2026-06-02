// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

func TestNextRefreshLabel(t *testing.T) {
	t.Parallel()

	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		next time.Time
		want string
	}{
		{name: "due-now", next: now, want: "due"},
		{name: "past", next: now.Add(-time.Second), want: "due"},
		{name: "sub-second", next: now.Add(500 * time.Millisecond), want: "<1s"},
		{name: "seconds", next: now.Add(25 * time.Second), want: "25s"},
		{name: "minutes", next: now.Add(3 * time.Minute), want: "3m"},
		{name: "hours", next: now.Add(2 * time.Hour), want: "2h"},
		{name: "zero next reads as due", next: time.Time{}, want: "due"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, listpage.NextRefreshLabel(now, tc.next))
		})
	}
}

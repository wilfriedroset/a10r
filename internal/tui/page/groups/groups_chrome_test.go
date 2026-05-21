// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func newPage(t *testing.T) *Page {
	t.Helper()
	return New(Options{
		Styles: pagetest.Styles(t),
		Now:    func() time.Time { return fixedNow },
	})
}

// TestPage_TitleLoadingAffordance pins the spinner-led loading title
// across the two loading windows. Regression guard for LoadingTitle.
func TestPage_TitleLoadingAffordance(t *testing.T) {
	t.Parallel()

	t.Run("cold-start", func(t *testing.T) {
		t.Parallel()
		p := newPage(t)
		require.True(t, strings.HasSuffix(p.Title(), " loading groups…"),
			"cold-start title must end with the loading affordance, got %q", p.Title())
	})

	t.Run("during-refresh", func(t *testing.T) {
		t.Parallel()
		p := newPage(t)
		_, _ = p.Update(poll.DataMsg{Resource: []backend.AlertGroup{}, Tenant: ""})
		require.Equal(t, "groups(all)[0]", p.Title(),
			"baseline title after first DataMsg has no loading affordance")
		_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
		require.True(t, strings.HasSuffix(p.Title(), " loading groups…"),
			"in-flight refresh re-enters the loading affordance, got %q", p.Title())
	})
}

// TestPage_FooterBranches pins all five branches of the refresh
// countdown. Regression guard for the C1 PollingUI swap and the C2
// RefreshCountdown collapse.
func TestPage_FooterBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setup func(*Page)
		want  string
	}{
		{
			name:  "pre-poll",
			setup: func(*Page) {},
			want:  "",
		},
		{
			name: "polled-with-next-refresh",
			setup: func(p *Page) {
				_, _ = p.Update(poll.DataMsg{
					Resource: []backend.AlertGroup{},
					Tenant:   "",
					NextAt:   fixedNow.Add(25 * time.Second),
				})
			},
			want: "next refresh 25s",
		},
		{
			name: "refreshing-in-flight",
			setup: func(p *Page) {
				_, _ = p.Update(poll.DataMsg{Resource: []backend.AlertGroup{}, Tenant: ""})
				_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			},
			want: "refreshing…",
		},
		{
			name: "paused",
			setup: func(p *Page) {
				_, _ = p.Update(poll.DataMsg{Resource: []backend.AlertGroup{}, Tenant: ""})
				_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
			},
			want: "WATCH OFF",
		},
		{
			name: "paused-and-refreshing",
			setup: func(p *Page) {
				_, _ = p.Update(poll.DataMsg{Resource: []backend.AlertGroup{}, Tenant: ""})
				_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
				_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			},
			want: "WATCH OFF · refreshing…",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPage(t)
			tc.setup(p)
			require.Equal(t, tc.want, p.Footer())
		})
	}
}

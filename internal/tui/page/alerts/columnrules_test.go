// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/session"
)

// The wide column adds Shift+W but stays out of columns(), and two
// tenants add TENANT, so the page shows every built-in it can.
func TestColumnRules_ConfigMatchesThePage(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Tenants: []string{"prod", "staging"},
		Scope:   "all",
		Session: session.New(withAlertColumns(config.Column{Label: "pod", Wide: true})),
	})
	titles := make([]string, 0, 6)
	for _, c := range p.columns() {
		titles = append(titles, c.Title)
	}

	require.Equal(t, []string{"TENANT", "SEVERITY", "ALERTNAME", "COUNT", "STATE", "AGE"}, titles)
	pagetest.RequireColumnRulesMatchPage(t, withAlertColumns, titles, p.Bindings())
}

// Wide is on so columns() renders every declared column; the region
// column has no sort_key, so it renders but is not an axis.
func TestSortAxes_FollowTheSortableColumns(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t,
		config.Column{Label: "cluster", SortKey: "K"},
		config.Column{Label: "region"},
		config.Column{Label: "pod", Wide: true, SortKey: "P"},
	)
	_, _ = p.Update(keyWide)
	var want []string
	for _, c := range p.columns() {
		if c.Sortable {
			want = append(want, c.Key)
		}
	}
	got := make([]string, 0, len(want))
	for _, c := range p.sortAxes() {
		got = append(got, c.Key)
	}

	require.Equal(t, []string{"severity", "alertname", "label:cluster", "label:pod", "count", "age"}, want)
	require.Equal(t, want, got)
	require.Len(t, got, len(alertSortColumns(p.labels.All())), "an axis has no sortable column")
}

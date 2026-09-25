// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/session"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func withGroupColumns(cols ...config.Column) config.Config {
	return config.Config{Pages: config.PageOverrides{GroupDetail: config.GroupDetailConfig{Columns: cols}}}
}

func reloadablePage(t *testing.T, cols ...config.Column) (*Page, *session.Session) {
	t.Helper()
	sess := session.New(withGroupColumns(cols...))
	p := New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Instances: []backend.Alert{podInstance("web-0", "p0"), podInstance("web-1", "p1")},
		Session:   sess,
	})
	return p, sess
}

func TestConfigReloadedRebuildsColumns(t *testing.T) {
	t.Parallel()

	p, sess := reloadablePage(t, config.Column{Label: podKey})
	require.Contains(t, testutil.StripStyle(p.View(120, 24)), "POD")

	sess.Apply(withGroupColumns(config.Column{Label: "cluster", Title: "fleet"}))
	_, _ = p.Update(app.ConfigReloadedMsg{})

	out := testutil.StripStyle(p.View(120, 24))
	require.Contains(t, out, "FLEET")
	require.NotContains(t, out, "POD")
	require.Contains(t, rowContaining(t, out, "web-1"), "ops", "the cells come from the new label")
}

func TestConfigReloadedDropsMissingSortAxis(t *testing.T) {
	t.Parallel()

	p, sess := reloadablePage(t, config.Column{Label: podKey, SortKey: "L"})
	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()

	sess.Apply(config.Config{})
	require.NotPanics(t, func() { _, _ = p.Update(app.ConfigReloadedMsg{}) })

	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.NotPanics(t, func() { _ = p.View(120, 24) })
}

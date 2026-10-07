// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/session"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func withAlertColumns(cols ...config.Column) config.Config {
	return config.Config{Pages: config.PageOverrides{Alerts: config.AlertsPageConfig{Columns: cols}}}
}

func reloadablePage(t *testing.T, cols ...config.Column) (*Page, *session.Session) {
	t.Helper()
	sess := session.New(withAlertColumns(cols...))
	p := New(Options{Styles: pagetest.Styles(t), Now: func() time.Time { return fixedNow }, Session: sess})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("Zeta", "fp1", "prod"),
		clusterAlert("Alpha", "fp2", "staging"),
	}})
	return p, sess
}

func TestConfigReloadedRebuildsColumns(t *testing.T) {
	t.Parallel()

	p, sess := reloadablePage(t, config.Column{Label: "cluster"})
	require.Contains(t, testutil.StripStyle(p.View(120, 24)), "CLUSTER")

	sess.Apply(withAlertColumns(config.Column{Label: "instance", Title: "host"}))
	_, _ = p.Update(app.ConfigReloadedMsg{})

	out := testutil.StripStyle(p.View(120, 24))
	require.Contains(t, out, "HOST")
	require.NotContains(t, out, "CLUSTER")
	require.Contains(t, rowContaining(t, out, "Zeta"), "fp1", "the cells come from the new label")
	require.NotContains(t, rowContaining(t, out, "Zeta"), "prod")

	sess.Apply(config.Config{})
	_, _ = p.Update(app.ConfigReloadedMsg{})
	require.NotContains(t, testutil.StripStyle(p.View(120, 24)), "HOST")
}

// The wide tier is page state, so a reload that removes the last wide
// column must switch it off: otherwise a later reload that adds one
// back would open it already shown, with no Shift+W press behind it.
func TestConfigReloadedResetsTheWideTier(t *testing.T) {
	t.Parallel()

	wide := config.Column{Label: "cluster", Wide: true}
	p, sess := reloadablePage(t, wide)
	require.True(t, p.labels.ToggleWide())

	sess.Apply(config.Config{})
	_, _ = p.Update(app.ConfigReloadedMsg{})
	sess.Apply(withAlertColumns(wide))
	_, _ = p.Update(app.ConfigReloadedMsg{})

	require.NotContains(t, testutil.StripStyle(p.View(120, 24)), "CLUSTER", "the wide column starts hidden")
}

func TestConfigReloadedDropsMissingSortAxis(t *testing.T) {
	t.Parallel()

	p, sess := reloadablePage(t, config.Column{Label: "cluster", SortKey: "L"})
	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()

	sess.Apply(config.Config{})
	require.NotPanics(t, func() { _, _ = p.Update(app.ConfigReloadedMsg{}) })

	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.NotPanics(t, func() { _ = p.View(120, 24) })
}

// poll_delta is read when a poll lands, so a reload that switches it
// on reaches the page already open.
func TestPollDeltaFollowsApply(t *testing.T) {
	t.Parallel()

	sess := session.New(config.Config{})
	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Tenants: []string{"prod"},
		Session: sess,
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: deltaAlerts()})

	sess.Apply(config.Config{TUI: config.TUI{PollDelta: true}})
	_, cmd := p.Update(poll.DataMsg{Tenant: "prod", Resource: deltaAlerts("HighCPU")})

	requireFlash(t, cmd, "+1 new", footer.FlashWarn)
}

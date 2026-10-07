// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var (
	keyWide = tea.KeyPressMsg{Code: 'W', Text: "W", Mod: tea.ModShift}
	// keySortPod drives the sort through Update rather than calling
	// the sorter directly, so the test covers the page's own
	// handleSort / recompute path as well.
	keySortPod = tea.KeyPressMsg{Code: 'L', Text: "L", Mod: tea.ModShift}
)

const clusterKey = "cluster"

// widePage is the group-detail page with one plain and one wide
// column over two instances.
func widePage(t *testing.T, sortKey string) *Page {
	t.Helper()
	return newColumnPage(t,
		[]config.Column{
			{Label: clusterKey},
			{Label: podKey, Wide: true, SortKey: sortKey},
		},
		wideInstance("fp-1", "eu-1", "web-a"),
		wideInstance("fp-2", "us-9", "web-b"),
	)
}

func wideInstance(fp, cluster, pod string) backend.Alert {
	a := podInstance(fp, pod)
	a.Labels[clusterKey] = cluster
	return a
}

func TestWideTier_WideColumnAppearsOnlyAfterShiftW(t *testing.T) {
	t.Parallel()

	p := widePage(t, "")

	// The pod value is counted, not searched for: the flex INSTANCE
	// cell lists every distinguishing label, so it names the pod on
	// both tiers. Only the second occurrence is the POD column.
	out := testutil.StripStyle(p.View(160, 20))
	require.Contains(t, out, "CLUSTER")
	require.NotContains(t, out, "POD")
	require.Equal(t, 1, strings.Count(rowContaining(t, out, "fp-1"), "web-a"))

	_, _ = p.Update(keyWide)
	out = testutil.StripStyle(p.View(160, 20))
	require.Contains(t, out, "POD")
	require.Equal(t, 2, strings.Count(rowContaining(t, out, "fp-1"), "web-a"))

	_, _ = p.Update(keyWide)
	require.NotContains(t, testutil.StripStyle(p.View(160, 20)), "POD")
}

// Hiding a column must not slide the remaining cells under the wrong
// headers, so the wide column goes in the middle here.
func TestWideTier_RemainingCellsStayUnderTheirHeaders(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t,
		[]config.Column{
			{Label: clusterKey},
			{Label: podKey, Wide: true},
			{Label: "team"},
		},
		withTeam(wideInstance("fp-1", "eu-1", "web-a"), "sre"),
		withTeam(wideInstance("fp-2", "us-9", "web-b"), "net"),
	)

	out := testutil.StripStyle(p.View(180, 20))
	header, row := rowContaining(t, out, "CLUSTER"), rowContaining(t, out, "fp-1")
	require.Equal(t, cellStart(t, header, "CLUSTER"), cellStart(t, row, "eu-1"))
	require.Equal(t, cellStart(t, header, "TEAM"), cellStart(t, row, "sre"))
}

func withTeam(a backend.Alert, team string) backend.Alert {
	a.Labels["team"] = team
	return a
}

func TestWideTier_BindingOnlyWhenAWideColumnExists(t *testing.T) {
	t.Parallel()

	require.True(t, hasBinding(widePage(t, "").Bindings(), "Shift+W"))
	require.False(t, hasBinding(newColumnPage(t, []config.Column{{Label: clusterKey}}).Bindings(), "Shift+W"))
}

func TestWideTier_HiddenColumnIsNotASortAxis(t *testing.T) {
	t.Parallel()

	p := widePage(t, "L")
	require.False(t, hasBinding(p.Bindings(), "Shift+L"))
	require.False(t, p.sorter.HandleKey("L"))
	for range 8 {
		p.sorter.WalkRight()
		require.NotEqual(t, "label:pod", p.sorter.ActiveKey())
	}

	_, _ = p.Update(keyWide)
	require.True(t, hasBinding(p.Bindings(), "Shift+L"))
	require.True(t, p.sorter.HandleKey("L"))
}

// A sort on a wide column is parked, not lost.
func TestWideTier_SortOnAWideColumnParksAndReturns(t *testing.T) {
	t.Parallel()

	// The severity order and the pod order are opposites here, so
	// every step below reads a different row order rather than only a
	// different active key.
	critical := wideInstance("fp-1", "eu-1", "z-pod")
	critical.Labels["severity"] = "critical"
	p := newColumnPage(t,
		[]config.Column{
			{Label: clusterKey},
			{Label: podKey, Wide: true, SortKey: "L"},
		},
		critical,
		wideInstance("fp-2", "us-9", "a-pod"),
	)
	require.Equal(t, []string{"eu-1|z-pod", "us-9|a-pod"}, podCellsOf(p))

	_, _ = p.Update(keyWide)
	_, _ = p.Update(keySortPod)
	require.Equal(t, "label:pod", p.sorter.ActiveKey())
	require.Equal(t, []string{"us-9|a-pod", "eu-1|z-pod"}, podCellsOf(p))

	// A second press flips the pod column to DESC. Both columns on
	// this page default to ASC, so a direction the parked sort must
	// not inherit only exists once the operator makes one.
	_, _ = p.Update(keySortPod)
	require.False(t, p.sorter.Asc())

	_, _ = p.Update(keyWide)
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc(), "a parked sort must read the default column's own direction")
	require.Equal(t, []string{"eu-1|z-pod", "us-9|a-pod"}, podCellsOf(p),
		"a parked sort must order the rows by the default column")

	_, _ = p.Update(keyWide)
	require.Equal(t, "label:pod", p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(), "un-hiding must restore the operator's own direction")
	require.Equal(t, []string{"eu-1|z-pod", "us-9|a-pod"}, podCellsOf(p))
}

// Shift+W on a page with no wide column changes nothing, so the
// page paints an identical frame.
func TestWideTier_KeyIsANoOpWithoutAWideColumn(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: clusterKey}},
		wideInstance("fp-1", "eu-1", "web-a"))

	before := p.View(160, 20)
	_, _ = p.Update(keyWide)
	require.Equal(t, before, p.View(160, 20))
}

// cellStart is the display column where the last occurrence of value
// begins. Counted in cells rather than bytes, because the cursor
// marker is one cell wide but three bytes long, and the last
// occurrence because a flex cell can repeat the label values.
func cellStart(t *testing.T, line, value string) int {
	t.Helper()
	i := strings.LastIndex(line, value)
	require.GreaterOrEqual(t, i, 0, "%q is not in %q", value, line)
	return lipgloss.Width(line[:i])
}

func hasBinding(in []action.Action, key string) bool {
	for _, b := range in {
		if b.Key == key {
			return true
		}
	}
	return false
}

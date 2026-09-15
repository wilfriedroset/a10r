// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var (
	keyWide = tea.KeyPressMsg{Code: 'W', Text: "W", Mod: tea.ModShift}
	// keySortPod drives the sort through Update rather than calling
	// the sorter directly, so the test covers the page's own
	// handleSort / recompute path as well.
	keySortPod = tea.KeyPressMsg{Code: 'L', Text: "L", Mod: tea.ModShift}
)

// podAlert is one instance carrying both a plain and a wide label.
func podAlert(name, fp, cluster, pod string) backend.Alert {
	a := clusterAlert(name, fp, cluster)
	a.Labels["pod"] = pod
	return a
}

// widePage is the alerts page with one plain and one wide column.
func widePage(t *testing.T, sortKey string) *Page {
	t.Helper()
	p := newColumnPage(t,
		config.Column{Label: "cluster"},
		config.Column{Label: "pod", Wide: true, SortKey: sortKey},
	)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		podAlert("DiskFull", "fp1", "prod", "web-a"),
	}})
	return p
}

func TestWideTier_WideColumnAppearsOnlyAfterShiftW(t *testing.T) {
	t.Parallel()

	p := widePage(t, "")

	out := testutil.StripStyle(p.View(160, 24))
	require.Contains(t, out, "CLUSTER")
	require.NotContains(t, out, "POD")
	require.NotContains(t, rowContaining(t, out, "DiskFull"), "web-a")

	_, _ = p.Update(keyWide)
	out = testutil.StripStyle(p.View(160, 24))
	require.Contains(t, out, "POD")
	require.Contains(t, rowContaining(t, out, "DiskFull"), "web-a")

	_, _ = p.Update(keyWide)
	require.NotContains(t, testutil.StripStyle(p.View(160, 24)), "POD")
}

// Hiding a column must not slide the remaining cells under the wrong
// headers, so the wide column goes in the middle here.
func TestWideTier_RemainingCellsStayUnderTheirHeaders(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t,
		config.Column{Label: "cluster"},
		config.Column{Label: "pod", Wide: true},
		config.Column{Label: "team"},
	)
	a := podAlert("DiskFull", "fp1", "prod", "web-a")
	a.Labels["team"] = "sre"
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{a}})

	out := testutil.StripStyle(p.View(180, 24))
	header, row := rowContaining(t, out, "CLUSTER"), rowContaining(t, out, "DiskFull")
	require.Equal(t, cellStart(t, header, "CLUSTER"), cellStart(t, row, "prod"))
	require.Equal(t, cellStart(t, header, "TEAM"), cellStart(t, row, "sre"))
}

// Shift+W is advertised only when the page has a wide column,
// because otherwise the key changes nothing.
func TestWideTier_BindingOnlyWhenAWideColumnExists(t *testing.T) {
	t.Parallel()

	require.True(t, hasBinding(widePage(t, "").Bindings(), "Shift+W"))
	require.False(t, hasBinding(newColumnPage(t, config.Column{Label: "cluster"}).Bindings(), "Shift+W"))
}

// A hidden column is not a sort axis: its hotkey does nothing and the
// h/l walk steps over it.
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

// A sort on a wide column is parked, not lost: hiding the column
// falls back to the default sort, and showing it again restores the
// operator's choice.
func TestWideTier_SortOnAWideColumnParksAndReturns(t *testing.T) {
	t.Parallel()

	// The two rows disagree on both axes, so each assertion below
	// reads a different order. Severity defaults DESC and a label
	// column defaults ASC, which is what catches a parked sort that
	// keeps the hidden column's direction.
	p := newColumnPage(t,
		config.Column{Label: "cluster"},
		config.Column{Label: "pod", Wide: true, SortKey: "L"},
	)
	worst := mkAlert("DiskFull", "critical", backend.AlertStateActive, "fp1", time.Minute,
		map[string]string{"pod": "z-pod"})
	mild := mkAlert("Latency", "warning", backend.AlertStateActive, "fp2", time.Minute,
		map[string]string{"pod": "a-pod"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{worst, mild}})

	requireRowOrder(t, p, "DiskFull", "Latency")

	_, _ = p.Update(keyWide)
	_, _ = p.Update(keySortPod)
	require.Equal(t, "label:pod", p.sorter.ActiveKey())
	requireRowOrder(t, p, "Latency", "DiskFull")

	_, _ = p.Update(keyWide)
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(), "a parked sort must read severity's own direction")
	requireRowOrder(t, p, "DiskFull", "Latency")

	_, _ = p.Update(keyWide)
	require.Equal(t, "label:pod", p.sorter.ActiveKey())
	requireRowOrder(t, p, "Latency", "DiskFull")
}

// requireRowOrder asserts that the page paints the named alerts top
// to bottom in the order given.
func requireRowOrder(t *testing.T, p *Page, names ...string) {
	t.Helper()
	out := testutil.StripStyle(p.View(160, 24))
	at := make([]int, 0, len(names))
	for _, n := range names {
		i := strings.Index(out, n)
		require.GreaterOrEqual(t, i, 0, "%q is not rendered\n%s", n, out)
		at = append(at, i)
	}
	require.IsIncreasing(t, at, "rows are not in the order %v\n%s", names, out)
}

// Shift+W on a page with no wide column changes nothing, so the
// page paints an identical frame.
func TestWideTier_KeyIsANoOpWithoutAWideColumn(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "prod"),
	}})

	before := p.View(160, 24)
	_, _ = p.Update(keyWide)
	require.Equal(t, before, p.View(160, 24))
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

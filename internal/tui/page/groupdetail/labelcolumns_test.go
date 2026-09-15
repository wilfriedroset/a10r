// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func newColumnPage(t *testing.T, cols []config.Column, instances ...backend.Alert) *Page {
	t.Helper()
	return New(Options{
		Styles:    pagetest.Styles(t),
		Now:       func() time.Time { return fixedNow },
		Tenant:    tenant,
		AlertName: alertName,
		Instances: instances,
		Columns:   cols,
	})
}

// podInstance is one instance carrying an optional pod label — ""
// omits the label entirely, which is the empty-cell case.
func podInstance(fp, pod string) backend.Alert {
	extra := map[string]string{sortKeyInstance: fp}
	if pod != "" {
		extra[podKey] = pod
	}
	return instance(fp, "warning", backend.AlertStateActive, extra)
}

// A group-detail row is one instance, so the cell is the raw label
// value. There is no rollup and no marker here (ADR 0048).
func TestLabelColumn_RendersRawValue(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey}},
		podInstance("fp-1", "web-a"),
		podInstance("fp-2", "web-b"),
	)

	out := testutil.StripStyle(p.View(140, 20))
	require.Contains(t, out, "POD")
	require.Contains(t, rowContaining(t, out, "fp-1"), "web-a")
	require.Contains(t, rowContaining(t, out, "fp-2"), "web-b")
	require.NotContains(t, out, "values>")
}

// The label block sits between INSTANCE and STATE, which is the one
// ordering the spec pins for this page.
func TestLabelColumn_SitsBetweenInstanceAndState(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey, Title: "WORKLOAD"}},
		podInstance("fp-1", "web-a"),
		podInstance("fp-2", "web-b"),
	)

	header := rowContaining(t, testutil.StripStyle(p.View(140, 20)), "INSTANCE")
	require.Less(t, strings.Index(header, "INSTANCE"), strings.Index(header, "WORKLOAD"))
	require.Less(t, strings.Index(header, "WORKLOAD"), strings.Index(header, "STATE"))
}

func TestLabelColumn_SortsByHotkey(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey, SortKey: "L"}},
		podInstance("fp-1", "web-c"),
		podInstance("fp-2", "web-a"),
		podInstance("fp-3", "web-b"),
	)

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"web-a", "web-b", "web-c"}, podCellsOf(p))

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"web-c", "web-b", "web-a"}, podCellsOf(p))
}

// An instance with no such label sorts last in both directions:
// unknown is not the smallest value.
func TestLabelColumn_EmptyCellsSortLast(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey, SortKey: "L"}},
		podInstance("fp-1", "web-b"),
		podInstance("fp-2", ""),
		podInstance("fp-3", "web-a"),
	)

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"web-a", "web-b", ""}, podCellsOf(p))

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"web-b", "web-a", ""}, podCellsOf(p))
}

// A column with no sort_key renders but is not a sort axis, so the
// h/l walk must never land on it. Severity carries no hotkey on this
// page by design, so the walk does visit hotkey-less built-ins.
func TestLabelColumn_WithoutSortKeyIsNotASortAxis(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey}}, podInstance("fp-1", "web-a"))
	for _, b := range p.Bindings() {
		require.NotEqual(t, "Shift+L", b.Key)
	}
	for range 8 {
		p.sorter.WalkRight()
		require.NotEqual(t, "label:"+podKey, p.sorter.ActiveKey())
	}
}

func TestLabelColumn_SortKeyAppearsInBindings(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey, SortKey: "L"}}, podInstance("fp-1", "web-a"))
	var found bool
	for _, b := range p.Bindings() {
		if b.Key == "Shift+L" {
			found = true
			require.Equal(t, "sort by pod", b.Description)
		}
	}
	require.True(t, found, "Shift+L must be advertised in the help registry")
}

func TestLabelColumn_FixedWidthEllipsizes(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, []config.Column{{Label: podKey, Title: "P", Width: 5}},
		podInstance("fp-1", "a-very-long-pod-name"),
		podInstance("fp-2", "another-long-pod-name"),
	)

	// The flex INSTANCE cell repeats pod= as a distinguishing label,
	// so the assertion is on the clipped form, not on the absence of
	// the full one.
	require.Contains(t, rowContaining(t, testutil.StripStyle(p.View(160, 20)), "fp-1"), "a-ve…")
}

// With no configured columns the page renders exactly as it did
// before the feature existed.
func TestLabelColumn_AbsentConfigChangesNothing(t *testing.T) {
	t.Parallel()

	in := []backend.Alert{podInstance("fp-1", "web-a"), podInstance("fp-2", "web-b")}

	plain := newPage(t, in...)
	configured := newColumnPage(t, nil, in...)

	require.Equal(t, plain.View(140, 20), configured.View(140, 20))
}

func podCellsOf(p *Page) []string {
	out := make([]string, 0, len(p.view))
	for _, e := range p.view {
		out = append(out, strings.Join(e.labelCells, "|"))
	}
	return out
}

// Two columns where only the second is sortable: the comparator has
// to index the config slice, not the sorter's own shorter slice. A
// single-column test passes either way.
func TestLabelColumn_SortsOnTheRightCellWhenAnEarlierColumnHasNoSortKey(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t,
		[]config.Column{{Label: "cluster"}, {Label: podKey, SortKey: "L"}},
		withCluster(podInstance("fp-1", "web-c"), "eu-1"),
		withCluster(podInstance("fp-2", "web-a"), "us-9"),
	)

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"us-9|web-a", "eu-1|web-c"}, podCellsOf(p))
}

func withCluster(a backend.Alert, cluster string) backend.Alert {
	a.Labels["cluster"] = cluster
	return a
}

// A long value must ellipsize inside its own column rather than push
// the allocator into the proportional shrink, which takes the
// built-in columns below their floors.
func TestLabelColumn_NarrowTerminalKeepsBuiltInFloors(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("long-pod-", 12)
	p := newColumnPage(t, []config.Column{{Label: podKey}},
		podInstance("fp-1", long),
		podInstance("fp-2", long+"-b"),
	)

	// SEVERITY, INSTANCE, pod, STATE, AGE.
	floors := []int{12, 10, labelColumnWidthFloor, 8, 12}
	widths, win := p.columnWidths(80)
	require.Len(t, win.Cols, win.Total, "every column must fit at this width")
	require.Len(t, widths, len(floors))
	for i, floor := range floors {
		require.GreaterOrEqual(t, widths[i], floor, "column %d fell below its floor", i)
	}
}

// Spec item 9: a vertical scroll must never change a column width.
// measureLabelColumns is called directly at two scroll positions
// because recompute resets the viewport before it measures, so a
// scroll-then-recompute test would pass against a window-local
// measure too. Only one row carries the wide value, and it is off
// screen at the top of the list.
func TestLabelColumn_ScrollDoesNotChangeMeasuredWidth(t *testing.T) {
	t.Parallel()

	in := make([]backend.Alert, 0, 40)
	for i := range 40 {
		pod := "web-a"
		if i == 20 {
			pod = "a-much-longer-pod-name"
		}
		in = append(in, podInstance("fp-"+strconv.Itoa(i), pod))
	}
	p := newColumnPage(t, []config.Column{{Label: podKey}}, in...)
	p.SetViewport(10, len(p.view))
	require.Equal(t, []int{lipgloss.Width("a-much-longer-pod-name")}, p.measureLabelColumns(),
		"the wide value must drive the width before any scroll")

	for range len(p.view) - 1 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Positive(t, p.TopRow(), "the scroll must move the window off the wide row")
	require.Equal(t, []int{lipgloss.Width("a-much-longer-pod-name")}, p.measureLabelColumns())
}

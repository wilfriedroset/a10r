// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func newColumnPage(t *testing.T, cols ...config.Column) *Page {
	t.Helper()
	return New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Columns: cols,
	})
}

// clusterAlert is one instance of name carrying an optional cluster
// label — "" omits the label entirely.
func clusterAlert(name, fp, cluster string) backend.Alert {
	extra := map[string]string{"instance": fp}
	if cluster != "" {
		extra["cluster"] = cluster
	}
	return mkAlert(name, "warning", backend.AlertStateActive, fp, time.Minute, extra)
}

// suppressedClusterAlert is clusterAlert in the state that paints a
// row dim, which is the state the marker must survive.
func suppressedClusterAlert(name, fp, cluster string) backend.Alert {
	a := clusterAlert(name, fp, cluster)
	a.State = backend.AlertStateSuppressed
	return a
}

func TestLabelColumn_RendersTitleAndValue(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "prod"),
	}})

	out := testutil.StripStyle(p.View(120, 24))
	require.Contains(t, out, "CLUSTER")
	require.Contains(t, rowContaining(t, out, "DiskFull"), "prod")
}

func TestLabelColumn_TitleOverrideWins(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster", Title: "fleet"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "prod"),
	}})

	out := testutil.StripStyle(p.View(120, 24))
	require.Contains(t, out, "FLEET")
	require.NotContains(t, out, "CLUSTER")
}

// An aggregate row shows the shared value when its instances agree
// and a distinct-count marker when they do not (ADR 0048).
func TestLabelColumn_AggregateRollup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		clusters []string
		want     string
	}{
		{name: "every instance agrees", clusters: []string{"prod", "prod"}, want: "prod"},
		{name: "instances disagree", clusters: []string{"prod", "staging"}, want: "<2 values>"},
		{name: "no instance has the label", clusters: []string{"", ""}, want: ""},
		{name: "partly missing counts as distinct", clusters: []string{"prod", ""}, want: "<2 values>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newColumnPage(t, config.Column{Label: "cluster"})
			alerts := make([]backend.Alert, 0, len(tt.clusters))
			for i, c := range tt.clusters {
				alerts = append(alerts, clusterAlert("DiskFull", string(rune('a'+i)), c))
			}
			_, _ = p.Update(poll.DataMsg{Resource: alerts})

			require.Len(t, p.groups, 1)
			require.Equal(t, []string{tt.want}, p.groups[0].labelCells)
		})
	}
}

// The rollup marker is text: it must not carry the dim style that
// marks a suppressed row.
func TestLabelColumn_MarkerIsPlainText(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		suppressedClusterAlert("DiskFull", "fp1", "prod"),
		suppressedClusterAlert("DiskFull", "fp2", "staging"),
	}})
	require.True(t, p.groups[0].allSuppressed(), "the dim path must be the one under test")

	out := p.View(120, 24)
	require.Contains(t, testutil.StripStyle(out), "<2 values>")
	// The marker must survive verbatim in the styled frame too: any
	// SGR sequence opening inside it would split the run.
	require.Contains(t, out, "<2 values>")
}

func TestLabelColumn_SortsByHotkey(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster", SortKey: "L"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("Zeta", "fp1", "prod"),
		clusterAlert("Alpha", "fp2", "staging"),
		clusterAlert("Mid", "fp3", "dev"),
	}})

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"dev", "prod", "staging"}, cellsOf(p))

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"staging", "prod", "dev"}, cellsOf(p))
}

// Markers rank after plain values, and empty cells stay last in both
// directions: an unknown label is not the smallest label.
func TestLabelColumn_SortOrdersMarkersAndEmpties(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster", SortKey: "L"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("Plain", "fp1", "prod"),
		clusterAlert("Empty", "fp2", ""),
		clusterAlert("Mixed", "fp3", "a"),
		clusterAlert("Mixed", "fp4", "b"),
	}})

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"prod", "<2 values>", ""}, cellsOf(p))

	require.True(t, p.sorter.HandleKey("L"))
	p.recompute()
	require.Equal(t, []string{"<2 values>", "prod", ""}, cellsOf(p))
}

func TestLabelColumn_SortKeyAppearsInBindings(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster", SortKey: "L"})
	var found bool
	for _, b := range p.Bindings() {
		if b.Key == "Shift+L" {
			found = true
			require.Equal(t, "sort by cluster", b.Description)
		}
	}
	require.True(t, found, "Shift+L must be advertised in the help registry")
}

// A column without a sort key is not a sort axis at all. It carries
// no Shift+<letter> entry in help, and the h/l walk never lands on
// it — walking onto it would make a column the operator declared
// unsortable the active sort and persist it to the state file.
func TestLabelColumn_WithoutSortKeyIsNotASortAxis(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster"})
	for _, b := range p.Bindings() {
		require.NotEqual(t, "Shift+L", b.Key)
	}
	for range 8 {
		p.sorter.WalkRight()
		require.NotEqual(t, "label:cluster", p.sorter.ActiveKey())
	}
}

// The label block sits between ALERTNAME and COUNT, so a second
// tenant shifts every downstream column index by one. Single-tenant
// tests cannot catch an off-by-one there.
func TestLabelColumn_MultiTenantKeepsColumnOrder(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:  pagetest.Styles(t),
		Now:     func() time.Time { return fixedNow },
		Tenants: []string{"prod", "staging"},
		Scope:   "all",
		Columns: []config.Column{{Label: "cluster"}},
	})
	_, _ = p.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "eu-1"),
	}})
	_, _ = p.Update(poll.DataMsg{Tenant: "staging", Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp2", "us-1"),
	}})

	out := testutil.StripStyle(p.View(160, 24))
	header := rowContaining(t, out, "ALERTNAME")
	require.Less(t, strings.Index(header, "ALERTNAME"), strings.Index(header, "CLUSTER"))
	require.Less(t, strings.Index(header, "CLUSTER"), strings.Index(header, "COUNT"))
	require.Contains(t, rowContaining(t, out, "prod"), "eu-1")
	require.Contains(t, rowContaining(t, out, "staging"), "us-1")
}

// Spec item 9: a vertical scroll must never change a column width.
// The widths are measured over the whole filtered view, not the
// visible window, and this pins that.
func TestLabelColumn_ScrollKeepsWidthsStable(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster"})
	in := make([]backend.Alert, 0, 40)
	for i := range 40 {
		name := "Alert" + strconv.Itoa(i)
		cluster := "eu-1"
		if i == 39 {
			cluster = "a-much-longer-cluster"
		}
		in = append(in, clusterAlert(name, "fp"+strconv.Itoa(i), cluster))
	}
	_, _ = p.Update(poll.DataMsg{Resource: in})

	widths := func() []int {
		cols := p.columns()
		l := p.scroll.Layout(cols, 160)
		out := make([]int, len(cols))
		for i, c := range cols {
			out[i] = l.WidthOf(c.Key)
		}
		return out
	}
	before := widths()
	for range 30 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Equal(t, before, widths())
}

// A measured label column flexes, so a long value ellipsizes inside
// its own column instead of reserving its full width and pushing the
// allocator into the proportional shrink, which takes the built-in
// columns below their floors. Two columns still fit an 80-cell
// terminal; a weight-0 label column broke the floors at one.
//
// The floors are spelled out here rather than imported because
// columns() keeps them function-local. A change there must land here
// too. ALERTNAME's is 11 rather than its declared 10: the layout pass
// floors every column at its own header plus the sort arrow.
func TestLabelColumn_NarrowTerminalKeepsBuiltInFloors(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("long-cluster-", 8)
	p := newColumnPage(t,
		config.Column{Label: "cluster"},
		config.Column{Label: "team"},
	)
	a := clusterAlert("DiskFull", "fp1", long)
	a.Labels["team"] = long
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{a}})

	// SEVERITY, ALERTNAME, cluster, team, COUNT, STATE, AGE.
	floors := []int{12, 11, labelColumnWidthFloor, labelColumnWidthFloor, 7, 14, 12}
	cols := p.columns()
	l := p.scroll.Layout(cols, 80)
	require.Len(t, cols, len(floors))
	for i, floor := range floors {
		require.GreaterOrEqual(t, l.WidthOf(cols[i].Key), floor, "column %d fell below its floor", i)
	}
	require.Contains(t, rowContaining(t, testutil.StripStyle(p.View(80, 24)), "DiskFull"), "…")
}

// Past the point where the floors all fit, the row scrolls rather
// than shrinking: it drops columns off the right edge and keeps every
// column it still paints at a readable width (ADR 0048).
func TestLabelColumn_TooNarrowScrollsInsteadOfShrinking(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("long-cluster-", 8)
	p := newColumnPage(t,
		config.Column{Label: "cluster"},
		config.Column{Label: "team"},
		config.Column{Label: "pod"},
	)
	a := clusterAlert("DiskFull", "fp1", long)
	a.Labels["team"] = long
	a.Labels["pod"] = long
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{a}})

	cols := p.columns()
	l := p.scroll.Layout(cols, 80)
	require.Zero(t, p.scroll.Offset)
	require.Positive(t, l.Shown())
	require.Less(t, l.Shown(), len(cols), "a row this narrow must drop columns")
	// Nothing has scrolled, so the window is a contiguous run from the
	// first column and the painted ones are the first Shown() of the set. One allocated
	// nothing is the shrink this test forbids.
	widths := make([]int, 0, l.Shown())
	for i, c := range cols[:l.Shown()] {
		w := l.WidthOf(c.Key)
		require.Positive(t, w, "column %d collapsed to nothing", i)
		widths = append(widths, w)
	}
	require.Contains(t, rowContaining(t, testutil.StripStyle(p.View(80, 24)), "SEVERITY"), ">")
	// One cell of the budget goes to the ">" marker.
	require.LessOrEqual(t, sum(widths)+len(widths)-1, 80-format.RowPrefixCols-1)
}

func sum(in []int) int {
	out := 0
	for _, v := range in {
		out += v
	}
	return out
}

func TestLabelColumn_FixedWidthEllipsizes(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster", Title: "C", Width: 5})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "a-very-long-cluster-name"),
	}})

	out := testutil.StripStyle(p.View(160, 24))
	row := rowContaining(t, out, "DiskFull")
	require.Contains(t, row, "a-ve…")
	require.NotContains(t, row, "a-very-long-cluster-name")
}

// With no configured columns the page renders exactly as it did
// before the feature existed.
func TestLabelColumn_AbsentConfigChangesNothing(t *testing.T) {
	t.Parallel()

	alerts := []backend.Alert{clusterAlert("DiskFull", "fp1", "prod")}

	plain := newPage(t)
	_, _ = plain.Update(poll.DataMsg{Resource: alerts})

	configured := newColumnPage(t)
	_, _ = configured.Update(poll.DataMsg{Resource: alerts})

	require.Equal(t, plain.View(120, 24), configured.View(120, 24))
}

func cellsOf(p *Page) []string {
	out := make([]string, 0, len(p.groups))
	for _, g := range p.groups {
		out = append(out, strings.Join(g.labelCells, "|"))
	}
	return out
}

// A sortable column whose widest cell is narrower than its title has
// to keep room for the sort arrow. Without the reserve the arrow is
// truncated away, and the arrow is the only thing that tells ASC from
// DESC (docs/end-users/keybindings.md).
func TestLabelColumn_NarrowSortableColumnKeepsItsArrow(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "env", SortKey: "L"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "prod"),
	}})
	p.groups[0].labelCells = []string{"dev"}
	require.True(t, p.sorter.HandleKey("L"))

	header := rowContaining(t, testutil.StripStyle(p.View(200, 24)), "ENV")
	require.Contains(t, header, "ENV ↑")
}

// The L2 page is built here, not by the boot factory, so this is the
// only path `pages.group_detail.columns` has to reach it. Without
// this the whole chain can be deleted and every other test stays
// green.
func TestLabelColumn_GroupDetailColumnsReachTheDrillDown(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles:             pagetest.Styles(t),
		Now:                func() time.Time { return fixedNow },
		GroupDetailColumns: []config.Column{{Label: "cluster", Title: "FLEET"}},
	})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("Multi", "fp1", "eu-1"),
		clusterAlert("Multi", "fp2", "us-1"),
	}})

	out := testutil.StripStyle(p.buildGroupPage(p.groups[0]).View(160, 20))
	require.Contains(t, out, "FLEET")
	require.Contains(t, rowContaining(t, out, "fp1"), "eu-1")
}

// The h/l walk steps the sorter's column order, so that order has to
// match the rendered one. The label block renders between ALERTNAME
// and COUNT, so a sorter that carries it after AGE sends `l` past the
// columns the operator sees and wraps onto them from the far end.
// STATE is rendered but is not a sort axis, so the walk order is the
// rendered order with STATE removed. Every column here declares a
// sort_key, which keeps the rest of the two orders identical.
func TestLabelColumn_WalkOrderMatchesRenderedOrder(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t,
		config.Column{Label: "cluster", SortKey: "L"},
		config.Column{Label: "team", SortKey: "Z"},
	)
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey(), "the walk starts at the default column")

	// The walk is cyclic, so the break is the real exit. The bound is
	// only a stop for a sorter that somehow never returns to its
	// start, and the column set is always the longer of the two.
	cols := p.columns()
	walk := []string{p.sorter.ActiveKey()}
	for range cols {
		p.sorter.WalkRight()
		if p.sorter.ActiveKey() == sortKeySeverity {
			break
		}
		walk = append(walk, p.sorter.ActiveKey())
	}

	rendered := make([]string, 0, len(walk))
	for _, c := range cols {
		if c.Sortable {
			rendered = append(rendered, c.Key)
		}
	}
	require.Equal(t, rendered, walk)
}

// A configured width may bound the cells; it may not cut the header.
// ADR 0048 makes the arrow the whole direction contract, so a column
// that renders "CLU" with no arrow lies about both its own identity
// and the live sort.
func TestLabelColumn_FixedWidthKeepsItsHeaderAndArrow(t *testing.T) {
	t.Parallel()

	p := newColumnPage(t, config.Column{Label: "cluster", Width: 3, SortKey: "L"})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		clusterAlert("DiskFull", "fp1", "prod"),
	}})
	require.True(t, p.sorter.HandleKey("L"))

	out := testutil.StripStyle(p.View(200, 24))
	require.Contains(t, rowContaining(t, out, "SEVERITY"), "CLUSTER ↑")
	require.Contains(t, rowContaining(t, out, "DiskFull"), "prod")
}

// A measured column asks for the flex floor, which is below the
// header width until Floor raises it, so the arrow goes the same way
// the pinned path lost it. The widths span the band where the window
// drops a different built-in column at each step, 79 among them
// because it is the most common terminal there is.
func TestLabelColumn_NarrowTerminalKeepsTheSortArrow(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("long-cluster-", 8)
	p := newColumnPage(t,
		config.Column{Label: "cluster", SortKey: "L"},
		config.Column{Label: "team"},
	)
	a := clusterAlert("DiskFull", "fp1", long)
	a.Labels["team"] = long
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{a}})
	require.True(t, p.sorter.HandleKey("L"))

	for _, width := range []int{55, 67, 70, 79, 82} {
		require.Contains(t, rowContaining(t, testutil.StripStyle(p.View(width, 24)), "SEVERITY"),
			"CLUSTER ↑", "width %d", width)
	}
}

// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/table"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func (p *Page) View(width, height int) string {
	l := p.scroll.Layout(p.columns(), width)
	return p.RenderListFrame(listpage.ListFrame{
		Width:      width,
		Height:     height,
		Now:        p.now(),
		CritColor:  p.styles.Severity.Critical.GetForeground(),
		Count:      len(p.groups),
		EmptyState: p.emptyState,
		Header:     func(int) string { return p.renderHeader(l) },
		Rows:       func(w, maxRows int) string { return p.renderRows(l, w, maxRows) },
	})
}

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.FilterBuffer() != "" || p.stateFilter != "" {
		return "no alerts match the active filter — Esc clears the prompt, Shift+F cycles state filters"
	}
	if !p.hasInScopeAlerts() {
		return "no alerts (yet) — the poller will refresh on the next tick"
	}
	return "no alerts in view"
}

// sortKeyState labels the STATE column. It is not a sort key — the
// breakdown column is non-sortable — but every column carries one and
// Layout.WidthOf addresses STATE by it.
const sortKeyState = "state"

// sortKeyTenant labels the TENANT column, on the same terms as
// sortKeyState.
const sortKeyTenant = "tenant"

// renderHeader returns the column-title row. The two foreground
// renderers are fg-only so the header keeps the terminal default
// background: painting a palette background inside the unstyled body
// frame creates a coloured stripe. The active column takes the
// second tint, which pairs with the arrow glyph to give two cues for
// which sort is live — one for the eye scanning columns, one for the
// eye reading the arrow.
func (p *Page) renderHeader(l table.Layout) string {
	return l.Header(
		table.Sort{Arrow: p.sorter.ArrowFor, Active: p.sorter.IsActive},
		table.Chrome{Fg: p.styles.Table.HeaderFg, ActiveFg: p.styles.Table.HeaderActiveFg},
	)
}

// renderRows returns the visible window of data rows. The window is
// reconciled against the cursor on every frame so the cursor stays
// inside it: scrolling down when the cursor walks past the bottom, up
// when it walks past the top.
func (p *Page) renderRows(l table.Layout, width, maxRows int) string {
	if maxRows <= 0 || len(p.groups) == 0 {
		return ""
	}
	end := min(p.TopRow()+maxRows, len(p.groups))
	ctx := rowCtx{
		showTenant: p.ShowTenantColumn(len(p.byTenant)),
		// STATE's allocated width caps the breakdown so an over-cap
		// breakdown ellipsizes here rather than starving ALERTNAME
		// (the cap lives in columns()). Zero means STATE scrolled out
		// of view, which disables the ellipsis.
		stateWidth: l.WidthOf(sortKeyState),
		spans:      p.FilterSpans(),
		// An open visual range previews as marked rows; the keys only
		// reach p.marks on commit, so the span is resolved per frame.
		visual: listpage.VisualPreview(&p.Base, p.groups, markKey),
	}
	var b strings.Builder
	// Reserve enough capacity for the visible page (rows x width) plus
	// per-row styling overhead so the Builder doesn't realloc while
	// every row appends. Multiplying by 2 covers the SGR bytes
	// lipgloss.Render injects per cell on coloured rows.
	b.Grow((end - p.TopRow()) * width * 2)
	for i := p.TopRow(); i < end; i++ {
		b.WriteString(l.Row(p.row(i, p.groups[i], ctx), width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// rowCtx carries the per-frame values, hoisted so the row loop does
// not recompute them.
type rowCtx struct {
	spans      func(string) [][2]int
	stateWidth int
	// visual is the open range's preview span; the zero value covers
	// no row.
	visual     listpage.VisualRange
	showTenant bool
}

// row builds one alert-group row at view index i. Per-cell colour
// (severity tint, per-token state colour) applies only to plain rows:
// cursor / marked / all-suppressed rows wrap the whole line in a
// row-level style, and nested ANSI inside that wrap is fragile, so
// cell-level colour is skipped there.
func (p *Page) row(i int, g alertGroup, ctx rowCtx) table.Row {
	ageLabel := p.formatTime(g.oldestStart)
	if ageLabel == "" {
		ageLabel = "—"
	}
	_, marked := p.marks[g.key()]
	marked = marked || ctx.visual.Covers(i)
	mark := " "
	if marked {
		mark = "✓"
	}
	rowStyled := i == p.Index() || marked || g.allSuppressed()
	hl := format.HighlighterFor(ctx.spans, p.styles.Table.MatchFg, rowStyled)
	// Bound once: a method value per cell would escape to the heap on
	// every painted cell of every row in the frame. Every cell takes
	// it, including the ones a producer already coloured — the
	// highlighter stands down on text carrying an escape byte rather
	// than painting over it.
	paint := hl.Text
	sevLabel := backend.SeverityLabel(g.severityRank)
	sevCell := hl.Text(sevLabel)
	if !rowStyled {
		sevCell = hl.Cell(sevLabel, p.styles.Severity.ForLabel(sevLabel))
	}
	cells := make([]table.Cell, 0, 6+len(p.shownCols))
	if ctx.showTenant {
		cells = append(cells, table.Cell{Text: g.tenant, Paint: paint})
	}
	cells = append(cells,
		table.Cell{Text: sevCell, Paint: paint},
		table.Cell{Text: alertNameCell(g), Paint: paint},
	)
	for _, c := range p.shownCols {
		cells = append(cells, table.Cell{Text: labelCellAt(&g, c.Index), Paint: paint})
	}
	cells = append(cells,
		table.Cell{Text: countCell(g), Paint: paint},
		table.Cell{Text: p.stateCell(g, ctx, rowStyled, hl), Paint: paint},
		table.Cell{Text: ageLabel, Paint: paint},
	)
	prefix := "  "
	if i == p.Index() {
		prefix = "▸ "
	}
	return table.Row{Prefix: prefix + mark + " ", Cells: cells, Style: p.rowStyle(i, g, marked)}
}

// rowStyle returns the style wrapping the whole row, or the zero
// style for a plain one. Precedence: cursor > marked > dimmed. Cursor
// wraps in fg+bg (the "you are here" signal); marked and dimmed change
// the foreground only so the row keeps the body background — k9s
// "tinted text". Dimmed fires only when every instance is suppressed,
// and marked beats it because it is an explicit user action while
// suppression is ambient state.
func (p *Page) rowStyle(i int, g alertGroup, marked bool) lipgloss.Style {
	switch {
	case i == p.Index():
		// k9s parity: cursor bg tracks the row's semantic colour (max
		// severity), not a static cursor colour.
		sev := p.styles.Severity.ForLabel(backend.SeverityLabel(g.severityRank))
		return p.styles.Table.CursorOver(sev.GetForeground())
	case marked:
		return p.styles.Table.MarkedFg
	case g.allSuppressed():
		return p.styles.Table.DimmedFg
	}
	return lipgloss.Style{}
}

// noAlertNameCell is the placeholder for a group whose instances
// carry no `alertname` label — the synthetic empty-name aggregate.
const noAlertNameCell = "(no alertname)"

// alertNameCell is the ALERTNAME cell content: the group's alertname,
// or the placeholder when empty.
func alertNameCell(g alertGroup) string {
	if g.alertName == "" {
		return noAlertNameCell
	}
	return g.alertName
}

// countArrowMarker trails the COUNT cell of a single-instance group,
// signalling that Enter skips L2 and lands straight on the instance
// detail (L3).
const countArrowMarker = " →"

// countCell renders the COUNT cell — the instance tally, with a
// trailing arrow on single-instance groups so the Enter-skips-L2
// shortcut is visible at the row.
func countCell(g alertGroup) string {
	s := strconv.Itoa(g.count)
	if g.count == 1 {
		s += countArrowMarker
	}
	return s
}

// stateContentCap bounds the STATE column's requested width. The full
// 3-bucket breakdown (`9 active · 3 suppressed · 1 unprocessed`, ~38
// cells) is weight-0 and would otherwise demand its full measured
// width, starving the ALERTNAME flex column and driving the table into
// the allocator's emergency proportional shrink. 24 fits the common
// homogeneous form (`567 active`) and most 2-bucket cases; the 3-bucket
// full form exceeds it and ellipsizes instead of cannibalising
// ALERTNAME. The compact form (`9ac 3su 1un`) stays well under the cap.
const stateContentCap = 24

// columns is the rendered column order, declared once. It is the only
// place on this page that knows where the user-declared block splices
// into the built-ins, so appending a built-in after AGE cannot
// silently break a lookup elsewhere.
//
// Content widths come from the filtered and aggregated view, so the
// layout reacts to the data the operator is looking at: a long
// alertname widens the flex column on a wide terminal and ellipsizes
// on a narrow one rather than burning fixed cells. Header labels need
// no measuring here — the layout pass floors every column at its own
// header.
func (p *Page) columns() []table.Column {
	const (
		// SEVERITY values are short ("critical", "warning", "info");
		// 12 keeps the column readable at the minimum and matches the
		// width it had before the allocator existed.
		sevMin = 12
		// COUNT: a single-instance row adds the " →" marker, so 7
		// keeps the tally and the marker legible.
		countMin = 7
		stateMin = 14
		// AGE: relative ("5m ago") fits in 12; the absolute-time
		// formatter renders 19 cells ("2026-05-01 13:45:00") plus a
		// breathing space.
		ageRelMin = 12
		ageAbsMin = 20
		// ALERTNAME floor: 10 cells preserves the prior "tiny but
		// scannable" minimum on bizarrely narrow terminals.
		alertNameMin = 10
		// TENANT default floor matches the prior fixed width so
		// existing scopes keep their layout.
		tenantMin = 16
	)
	ageMin := ageRelMin
	if p.timeFormat == timerender.Absolute {
		ageMin = ageAbsMin
	}
	m := p.measure()

	out := make([]table.Column, 0, 6+len(p.shownCols))
	if p.ShowTenantColumn(len(p.byTenant)) {
		out = append(out, table.Column{Key: sortKeyTenant, Title: "TENANT", Min: tenantMin, Content: m.tenant})
	}
	out = append(out,
		table.Column{Key: sortKeySeverity, Title: "SEVERITY", Sortable: true, Min: sevMin, Content: m.sev},
		// ALERTNAME is the unbounded flex column: FlexUnbounded stops
		// the allocator capping it, so it takes every leftover cell on
		// a wide terminal even when every alertname in view is short.
		// Capping at the live max would leave dead space the operator
		// could otherwise spend on the labels they are scanning.
		table.Column{
			Key: sortKeyName, Title: "ALERTNAME", Sortable: true,
			Min: alertNameMin, Content: format.FlexUnbounded, Weight: 1, Clip: table.ClipEllipsis,
		},
	)
	out = append(out, p.labelColumns(m.label)...)
	return append(out,
		table.Column{Key: sortKeyCount, Title: "COUNT", Sortable: true, Min: countMin, Content: m.count},
		table.Column{Key: sortKeyState, Title: "STATE", Min: stateMin, Content: min(stateContentCap, max(stateMin, m.state))},
		table.Column{Key: sortKeyAge, Title: "AGE", Sortable: true, Min: ageMin, Content: ageMin},
	)
}

// measured is the widest cell each measured column holds in the
// current view.
type measured struct {
	label                     []int
	tenant, sev, count, state int
}

// measure walks the view once for every measured column together, so
// the frame stays one pass over the rows rather than one per column.
// ALERTNAME is absent on purpose: its Content is the FlexUnbounded
// sentinel, so a per-row max could never beat it. Nothing seeds a
// header width here: the layout pass floors every column at its own
// header, so a second copy of the titles would only rot.
func (p *Page) measure() measured {
	m := measured{label: p.labelWidths}
	for i := range p.groups {
		g := &p.groups[i]
		m.tenant = max(m.tenant, lipgloss.Width(g.tenant))
		m.sev = max(m.sev, lipgloss.Width(backend.SeverityLabel(g.severityRank)))
		m.count = max(m.count, lipgloss.Width(countCell(*g)))
		m.state = max(m.state, lipgloss.Width(stateBreakdownPlain(*g, p.stateFormat)))
	}
	return m
}

// labelColumns turns the measured widths into the user-declared
// block. A measured column flexes rather than reserving its full
// width: label values run long (a pod name, an instance URL), and a
// weight-0 request that wide pushes the allocator into its
// proportional shrink, which takes the built-in columns below their
// own floors. Flexing reserves only the floor and grows into what is
// left alongside ALERTNAME, so a long value ellipsizes instead of
// collapsing the row. A configured width pins the cells; the layout
// pass still floors the column at its own header (ADR 0048).
func (p *Page) labelColumns(widths []int) []table.Column {
	out := make([]table.Column, 0, len(p.shownCols))
	for i, c := range p.shownCols {
		col := table.Column{Key: c.Key, Title: c.Title, Sortable: c.Hotkey != 0, Clip: table.ClipEllipsis}
		if c.Width > 0 {
			col.Min, col.Content = c.Width, c.Width
		} else {
			w := 0
			if i < len(widths) {
				w = widths[i]
			}
			col.Min, col.Content, col.Weight = min(labelColumnWidthFloor, w), w, 1
		}
		out = append(out, col)
	}
	return out
}

// labelColumnWidthFloor is the narrowest a measured label column asks
// for before its own header floor is applied. Below this a value is
// an ellipsis and a character or two, which says less than an empty
// cell would.
const labelColumnWidthFloor = 6

// measureLabelColumns measures each user-declared column over the
// whole filtered view, so a vertical scroll never shifts a width. A
// column with a configured Width needs no measuring and is left at
// zero, because labelColumns pins it before it reads this slice.
// recompute calls this once per row change rather than the renderer
// calling it once per frame: the scan is O(rows x columns) and the
// widths only move when the rows do.
func (p *Page) measureLabelColumns() []int {
	if len(p.shownCols) == 0 {
		return nil
	}
	out := make([]int, len(p.shownCols))
	for i, c := range p.shownCols {
		if c.Width > 0 {
			continue
		}
		content := 0
		for j := range p.groups {
			if w := lipgloss.Width(labelCellAt(&p.groups[j], c.Index)); w > content {
				content = w
			}
		}
		out[i] = content
	}
	return out
}

// formatTime renders ts according to the page's active time
// format. Mirrors the silences / alert-detail formatters so the
// three views agree on how the toggle reads.
func (p *Page) formatTime(ts time.Time) string {
	return timerender.Display(p.timeFormat, p.now(), ts)
}

// stateBucket pairs a non-zero state tally with its rendering inputs.
type stateBucket struct {
	count int
	state backend.AlertState
}

// orderedBuckets returns the group's non-zero state tallies in the
// fixed active → suppressed → unprocessed order the breakdown renders
// in. The three buckets always sum to count.
func orderedBuckets(g alertGroup) []stateBucket {
	all := []stateBucket{
		{g.active, backend.AlertStateActive},
		{g.suppressed, backend.AlertStateSuppressed},
		{g.unprocessed, backend.AlertStateUnprocessed},
	}
	out := make([]stateBucket, 0, len(all))
	for _, b := range all {
		if b.count > 0 {
			out = append(out, b)
		}
	}
	return out
}

// stateToken renders one bucket's text per the active density. Full
// echoes the AM-native word (`9 active`); Compact emits count + the
// two-letter abbreviation (`9ac`), chosen to avoid colliding visually
// with the `s` / `S` silence verbs. Unknown states fall through to the
// full string in both modes so a non-conforming value stays legible.
func stateToken(count int, s backend.AlertState, f stateformat.Format) string {
	if f != stateformat.Compact {
		return fmt.Sprintf("%d %s", count, s)
	}
	switch s {
	case backend.AlertStateActive:
		return fmt.Sprintf("%dac", count)
	case backend.AlertStateSuppressed:
		return fmt.Sprintf("%dsu", count)
	case backend.AlertStateUnprocessed:
		return fmt.Sprintf("%dun", count)
	default:
		return fmt.Sprintf("%d%s", count, s)
	}
}

// stateTokenStyle returns the foreground-only style for a bucket's
// token. Active reads in the table's default foreground: every row
// here is a firing alert, so "active" is the baseline, not a status to
// flag — urgency lives in the SEVERITY column and the all-suppressed
// row-dim, and a green "active" would falsely read as healthy.
// Suppressed dims (receded), unprocessed takes the unknown-severity
// foreground. Every branch is fg-only so the chrome keeps the terminal
// default background (see feedback memory on chrome rendering).
func stateTokenStyle(s backend.AlertState, styles *theme.Styles) lipgloss.Style {
	switch s {
	case backend.AlertStateSuppressed:
		return styles.Table.DimmedFg
	case backend.AlertStateUnprocessed:
		return styles.Severity.Unknown
	default:
		return lipgloss.NewStyle()
	}
}

// stateBreakdownSep joins the breakdown tokens. Full uses the spaced
// middot the design pins (`9 active · 3 suppressed`); Compact uses a
// single space (`9ac 3su`).
func stateBreakdownSep(f stateformat.Format) string {
	if f == stateformat.Compact {
		return " "
	}
	return " · "
}

// stateBreakdownPlain renders the STATE breakdown without colour — the
// width-measurement form. Same token text and separator the coloured
// renderer produces, so measure sees the true cell width.
func stateBreakdownPlain(g alertGroup, f stateformat.Format) string {
	buckets := orderedBuckets(g)
	parts := make([]string, 0, len(buckets))
	for _, b := range buckets {
		parts = append(parts, stateToken(b.count, b.state, f))
	}
	return strings.Join(parts, stateBreakdownSep(f))
}

// stateCell renders the STATE cell for one group, ellipsizing the
// breakdown to the column's allocated width when the full string
// overflows it. The allocated width is capped in columns()
// (stateContentCap) so a wide 3-bucket breakdown can't starve
// ALERTNAME; here the rendered string is clipped to match.
//
// On overflow the cell is rendered plain (uncoloured) and ellipsized
// with format.Ellipsize: the per-token colours that renderStateBreakdown
// applies are not SGR-safe to slice mid-token, so the truncated form
// drops them rather than risk a dangling escape. When the breakdown
// fits (the common case and the always-true case for the compact
// form), the fully styled render is returned untouched.
func (p *Page) stateCell(g alertGroup, ctx rowCtx, rowStyled bool, hl format.Highlighter) string {
	if ctx.stateWidth > 0 {
		plain := stateBreakdownPlain(g, p.stateFormat)
		if lipgloss.Width(plain) > ctx.stateWidth {
			// Left plain on purpose: the table module paints the cut
			// text, so a span past the ellipsis is dropped rather than
			// moved.
			return format.Ellipsize(plain, ctx.stateWidth)
		}
	}
	return renderStateBreakdown(g, p.stateFormat, p.styles, rowStyled, hl)
}

// renderStateBreakdown renders the STATE cell's per-state tally: non-
// zero buckets only, fixed active → suppressed → unprocessed order,
// summing to count. On plain rows each token is foreground-tinted by
// state; on cursor / marked / all-suppressed rows the per-token colour
// is skipped (rowStyled=true) so the row-level style wins.
func renderStateBreakdown(g alertGroup, f stateformat.Format, styles *theme.Styles, rowStyled bool, hl format.Highlighter) string {
	buckets := orderedBuckets(g)
	parts := make([]string, 0, len(buckets))
	for _, b := range buckets {
		tok := stateToken(b.count, b.state, f)
		if rowStyled {
			parts = append(parts, hl.Text(tok))
			continue
		}
		parts = append(parts, hl.Cell(tok, stateTokenStyle(b.state, styles)))
	}
	return strings.Join(parts, stateBreakdownSep(f))
}

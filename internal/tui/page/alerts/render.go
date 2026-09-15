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
	"github.com/wilfriedroset/a10r/internal/tui/page/labelcol"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func (p *Page) View(width, height int) string {
	// The scroll keys need to know whether the row fits, and no width
	// reaches the page at key time.
	p.scroll.Width = width
	return p.RenderListFrame(listpage.ListFrame{
		Width:      width,
		Height:     height,
		Now:        p.now(),
		CritColor:  p.styles.Severity.Critical.GetForeground(),
		Count:      len(p.groups),
		EmptyState: p.emptyState,
		Header:     p.renderHeader,
		Rows:       p.renderRows,
	})
}

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.Filter != "" || p.stateFilter != "" {
		return "no alerts match the active filter — Esc clears the prompt, Shift+F cycles state filters"
	}
	if !p.hasInScopeAlerts() {
		return "no alerts (yet) — the poller will refresh on the next tick"
	}
	return "no alerts in view"
}

// renderHeader returns the column-title row with a sort marker
// on the active column. Titles are upper-cased and styled via
// theme.Table.Header (k9s-style yellow on base in catppuccin) so
// they stand apart from the data rows. A leading TENANT column
// appears when the active scope spans multiple backends.
// sortKeyState labels the STATE column header. It is NOT a sort key —
// the breakdown column is non-sortable — but the header renderer walks
// a uniform key list, so the label lives here alongside the real keys.
// ArrowFor / IsActive return empty / false for an unknown key, so the
// column renders plain.
const sortKeyState = "state"

// sortKeyTenant labels the TENANT column header, on the same terms as
// sortKeyState: not a sort axis, but the header renderer walks a
// uniform key list and headerTitle upper-cases it into "TENANT".
const sortKeyTenant = "tenant"

func (p *Page) renderHeader(width int) string {
	keys := p.renderedKeys()
	widths, win := p.columnWidths(width)
	// fg-only renderers so the header keeps the terminal default
	// background — painting palette bg inside the unstyled body
	// frame creates a coloured stripe (see feedback memory on
	// chrome rendering).
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg

	var b strings.Builder
	b.WriteString(format.ScrollPrefix(win.ClipLeft))
	for j, ci := range win.Cols {
		if j >= len(widths) || ci >= len(keys) {
			break
		}
		if j > 0 {
			b.WriteString(colSep)
		}
		k := keys[ci]
		label := p.headerTitle(k)
		if arrow := p.sorter.ArrowFor(k); arrow != "" {
			label = label + " " + arrow
		}
		padded := format.PadRight(label, widths[j])
		// Active column gets HeaderActive; the rest get the regular
		// Header foreground. The two tints plus the arrow glyph give
		// two distinct cues for "which sort is live" — one for the
		// eye scanning columns, one for the eye reading the arrow.
		if p.sorter.IsActive(k) {
			b.WriteString(activeFg.Render(padded))
		} else {
			b.WriteString(headerFg.Render(padded))
		}
	}
	if win.ClipRight {
		b.WriteString(format.ScrollRightMarker)
	}
	return b.String()
}

// stateColumnIndex locates STATE inside the painted window. STATE is
// second to last in columnSpecs, but a horizontally scrolled row
// renumbers its columns, so the lookup goes through the window. -1
// means STATE scrolled out of view.
func stateColumnIndex(win format.Window) int {
	if win.Total < 2 {
		return -1
	}
	for j, ci := range win.Cols {
		if ci == win.Total-2 {
			return j
		}
	}
	return -1
}

// renderedKeys is headerKeys with the optional TENANT column
// prepended, so entry i names the column at columnSpecs()[i]. The
// scrolled renderer addresses columns by that index, and headerKeys
// alone is off by one whenever TENANT shows.
func (p *Page) renderedKeys() []string {
	keys := p.headerKeys()
	if !p.ShowTenantColumn(len(p.byTenant)) {
		return keys
	}
	return append([]string{sortKeyTenant}, keys...)
}

// headerKeys is the rendered column order as sorter keys: the
// built-ins with the user-declared label columns spliced in after
// ALERTNAME. renderRow, padColumns and columnSpecs splice the block
// at the same point; this function is the header's copy of that
// order, not the shared source of it.
func (p *Page) headerKeys() []string {
	out := make([]string, 0, 5+len(p.shownCols))
	out = append(out, sortKeySeverity, sortKeyName)
	for _, c := range p.shownCols {
		out = append(out, c.Key)
	}
	return append(out, sortKeyCount, sortKeyState, sortKeyAge)
}

// headerTitle maps a rendered column key to its header text. A user
// column carries a configured title; a built-in is its own key,
// upper-cased.
func (p *Page) headerTitle(key string) string {
	for _, c := range p.labelCols {
		if c.Key == key {
			return c.Title
		}
	}
	return strings.ToUpper(key)
}

// renderRows returns the visible window of data rows. The window
// is reconciled against the cursor on every frame so the cursor
// stays inside it: scrolling down when the cursor walks past the
// bottom, up when it walks past the top.
//
// The cursor row is wrapped in the theme's Table.Cursor style so
// it stands out k9s-style — the background fills the full width
// of the body, not just the visible characters, by padding the
// rendered string to width before the style wraps it.
func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.groups) == 0 {
		return ""
	}
	end := min(p.TopRow()+maxRows, len(p.groups))

	showTenant := p.ShowTenantColumn(len(p.byTenant))
	// Compute column widths once per frame: the spec builder walks
	// the full view to measure max content widths, and re-running
	// it per row would turn the render into O(rows²) under a
	// storm. The header renderer makes its own call (one per
	// frame, not per row) so the cost lands once on the outer loop
	// either way.
	cols, win := p.columnWidths(width)
	// STATE's allocated width caps the breakdown so an over-cap
	// breakdown ellipsizes here rather than starving ALERTNAME (the
	// cap lives in columnSpecs). -1 (no STATE column visible)
	// disables ellipsis.
	stateIdx := stateColumnIndex(win)
	spans := p.FilterSpans()
	// An open visual range previews as marked rows; the keys only
	// reach p.marks on commit, so the span is resolved per frame.
	visual := listpage.VisualPreview(&p.Base, p.groups, markKey)
	var b strings.Builder
	// Reserve enough capacity for the visible page (rows × width)
	// plus per-row styling overhead so the Builder doesn't realloc
	// while every row appends. Multiplying by 2 covers the SGR
	// bytes lipgloss.Render injects per cell on coloured rows.
	b.Grow((end - p.TopRow()) * width * 2)
	for i := p.TopRow(); i < end; i++ {
		b.WriteString(p.renderRow(i, p.groups[i], rowCtx{
			cols:       cols,
			win:        win.Cols,
			stateIdx:   stateIdx,
			width:      width,
			showTenant: showTenant,
			spans:      spans,
			visual:     visual,
		}))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// rowCtx carries the per-frame values, hoisted so the row loop does
// not recompute them.
type rowCtx struct {
	cols []int
	// win holds the columnSpecs indices the row paints, in order. It
	// is every column unless the row is scrolled horizontally.
	win      []int
	spans    func(string) [][2]int
	stateIdx int
	width    int
	// visual is the open range's preview span; the zero value covers
	// no row.
	visual     listpage.VisualRange
	showTenant bool
}

// renderRow renders one alert-group row at view index i, padded to
// width and styled. Per-cell colour (severity tint, per-token state
// colour) applies only to plain rows: cursor / marked / all-suppressed
// rows wrap the whole line in a row-level style, and nested ANSI inside
// that wrap is fragile, so cell-level colour is skipped there. Row
// precedence: cursor > marked > dimmed. Cursor wraps in fg+bg (the
// "you are here" signal); Marked and Dimmed change the foreground only
// so the row keeps the body background — k9s "tinted text". Dimmed
// fires only when every instance is suppressed and the row is neither
// cursor nor marked; Marked beats dimmed because it is an explicit
// user action while suppression is ambient state.
func (p *Page) renderRow(i int, g alertGroup, ctx rowCtx) string {
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
	sevLabel := backend.SeverityLabel(g.severityRank)
	sevCell := hl.Text(sevLabel)
	if !rowStyled {
		sevCell = hl.Cell(sevLabel, p.styles.Severity.ForLabel(sevLabel))
	}
	stateCell := p.stateCell(g, ctx, rowStyled, hl)
	row := make([]string, 0, 6+len(p.shownCols))
	if ctx.showTenant {
		row = append(row, g.tenant)
	}
	row = append(row, sevCell, alertNameCell(g))
	for _, c := range p.shownCols {
		row = append(row, labelCellAt(&g, c.Index))
	}
	row = append(row,
		countCell(g),
		stateCell,
		ageLabel,
	)
	prefix := "  "
	if i == p.Index() {
		prefix = "▸ "
	}
	line := format.PadRight(prefix+mark+" "+p.padColumns(row, ctx, hl), ctx.width)
	switch {
	case i == p.Index():
		// k9s parity: cursor bg tracks the row's semantic colour
		// (max severity), not a static cursor colour.
		rowColor := p.styles.Severity.ForLabel(backend.SeverityLabel(g.severityRank)).GetForeground()
		line = p.styles.Table.CursorOver(rowColor).Render(line)
	case marked:
		line = p.styles.Table.MarkedFg.Render(line)
	case g.allSuppressed():
		line = p.styles.Table.DimmedFg.Render(line)
	}
	return line
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

// colSep is the rendered inter-column separator string.
const colSep = " "

// stateContentCap bounds the STATE column's requested width. The full
// 3-bucket breakdown (`9 active · 3 suppressed · 1 unprocessed`, ~38
// cells) is weight-0 and would otherwise demand its full measured
// width, starving the ALERTNAME flex column and driving the table into
// the allocator's emergency proportional shrink. 24 fits the common
// homogeneous form (`567 active`) and most 2-bucket cases; the 3-bucket
// full form exceeds it and ellipsizes instead of cannibalising
// ALERTNAME. The compact form (`9ac 3su 1un`) stays well under the cap.
const stateContentCap = 24

// padColumns lays out the row's columns at pre-computed cols
// widths. The leading TENANT column is optional — added when
// scope spans multiple backends — so a row carries 4 or 5 built-in
// cells plus one per user-declared label column. The alertname
// column is the flex slot: when its assigned
// width is narrower than the label, the cell is ellipsized with
// format.Ellipsize so the truncation appends the EllipsizeSuffix
// ("…") and reads as intentional rather than as a silent slice.
// Other columns fall back to
// PadRight (which truncates on overflow without an ellipsis) —
// those columns rarely exceed their floor in practice and the
// ellipsis on a 1-cell shortfall would steal the only remaining
// content cell.
//
// cols comes from columnWidths and is computed once per View() so
// the row loop runs in O(rows) rather than O(rows²) — the spec
// builder walks the whole view to measure max content widths,
// and re-running it per row would scale badly under a storm.
func (p *Page) padColumns(parts []string, ctx rowCtx, hl format.Highlighter) string {
	flexIdx := p.flexColumnIndex()
	var b strings.Builder
	for j, ci := range ctx.win {
		if j >= len(ctx.cols) || ci >= len(parts) {
			break
		}
		if j > 0 {
			b.WriteString(colSep)
		}
		v := parts[ci]
		// The flex column and every user-declared column ellipsize:
		// both can be assigned less than their content, and a silent
		// slice of a label value reads as a different value.
		if ci == flexIdx || p.isLabelColumn(ci) {
			v = format.Ellipsize(v, ctx.cols[j])
		}
		// The cells a producer already coloured (SEVERITY, STATE) come
		// in styled and carry their own highlight; Text stands down on
		// them. The rest are plain and get painted here, after the pad
		// or the cut, so a span never moves a column.
		b.WriteString(hl.Text(format.PadRight(v, ctx.cols[j])))
	}
	return b.String()
}

// flexColumnIndex returns the position of the alertname column in
// columnSpecs. When the TENANT column is hidden the flex column sits
// at index 1 (after SEVERITY); when shown, at index 2. The index is
// spec-space, not window-space, so a scrolled row still identifies
// the column it belongs to.
// Centralised so padColumns and any future per-cell styler agree
// on which column is the unbounded one.
func (p *Page) flexColumnIndex() int {
	if p.ShowTenantColumn(len(p.byTenant)) {
		return 2
	}
	return 1
}

// isLabelColumn reports whether columnSpecs index i is one of the
// user-declared label columns — the block that follows the flex
// ALERTNAME column. Spec-space, like flexColumnIndex.
func (p *Page) isLabelColumn(i int) bool {
	flexIdx := p.flexColumnIndex()
	return i > flexIdx && i <= flexIdx+len(p.shownCols)
}

// columnWidths returns the per-column widths (TENANT optional,
// then SEVERITY, ALERTNAME flex, COUNT, STATE, AGE) by measuring
// the active dataset and handing the result to the duf-style
// distributor in package format. ALERTNAME is the unbounded
// (weight=1) flex column; the rest are weight=0 fixed columns
// that never grow past max(min, content). Per-row content widths
// come from the filtered+aggregated view so the layout reacts to
// the data the user is actually looking at — long alertnames trigger
// a wider flex column on a wide terminal and ellipsize on a
// narrow one rather than burning fixed cells.
//
// Header labels participate in the content measurement so the
// title row never gets clipped below its own glyph count (e.g.
// "ALERTNAME" is wider than a 3-char alertname).
func (p *Page) columnWidths(width int) ([]int, format.Window) {
	specs := p.columnSpecs()
	// Subtract the row prefix from total before distributing — the
	// allocator's contract is "fits in N cells", not "fits in N
	// minus chrome". Centralising the chrome subtraction here keeps
	// the spec construction pure and easy to test.
	budget := max(0, width-format.RowPrefixCols)
	win := format.WindowAt(specs, budget, len(colSep), p.scroll.Offset)
	if len(win.Cols) < win.Total {
		// A clipped row keeps one cell out of the column budget for the
		// ">" marker, so the header never runs past the body width and
		// wraps. The cell goes unpainted on a row clipped only on the
		// left, where the "<" marker rides the row prefix instead:
		// reserving it on ClipRight alone would be circular, because
		// the smaller budget is what decides ClipRight. Re-running the
		// window on the smaller budget can only drop a further column,
		// never bring one back, so the result is stable.
		budget = max(0, budget-1)
		win = format.WindowAt(specs, budget, len(colSep), p.scroll.Offset)
	}
	shown := make([]format.Column, len(win.Cols))
	for i, ci := range win.Cols {
		shown[i] = specs[ci]
	}
	return format.Distribute(shown, budget, len(colSep)), win
}

// columnSpecs builds the per-column Spec slice the distributor
// consumes. Centralised so the header renderer, the row renderer,
// and tests share one source of truth on which columns exist and
// how they flex.
func (p *Page) columnSpecs() []format.Column {
	const (
		// SEVERITY values: severity labels are short ("critical",
		// "warning", "info"); 12 keeps the column readable at the
		// minimum and matches the previous fixed width so existing
		// snapshots don't shift on the happy path.
		sevMin = 12
		// COUNT floor: "COUNT" header is 5 cells; a single-instance
		// row adds the " →" marker, so 7 keeps both legible.
		countMin = 7
		stateMin = 14
		// AGE: relative ("5m ago") fits in 12; the absolute-time
		// formatter renders 19 cells ("2026-05-01 13:45:00") plus a
		// breathing space — the column floor lifts to 20 in that
		// mode so the timestamp never overflows.
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

	// Measure max content width per column from the live dataset.
	// Header labels are included so a column never collapses under
	// its own title. ALERTNAME is intentionally absent — its
	// Content is the format.FlexUnbounded sentinel, so the per-row max
	// would never beat the cap and walking it every frame is dead
	// work for nothing. STATE now measures the rendered breakdown
	// (wider than a bare state) and COUNT the digit count plus the
	// single-instance arrow marker.
	var (
		tenantContent = lipgloss.Width("TENANT")
		sevContent    = lipgloss.Width("SEVERITY")
		countContent  = lipgloss.Width("COUNT")
		stateContent  = lipgloss.Width("STATE")
		ageContent    = lipgloss.Width("AGE")
	)
	for _, g := range p.groups {
		if w := lipgloss.Width(g.tenant); w > tenantContent {
			tenantContent = w
		}
		if w := lipgloss.Width(backend.SeverityLabel(g.severityRank)); w > sevContent {
			sevContent = w
		}
		if w := lipgloss.Width(countCell(g)); w > countContent {
			countContent = w
		}
		if w := lipgloss.Width(stateBreakdownPlain(g, p.stateFormat)); w > stateContent {
			stateContent = w
		}
	}
	// AGE content width is bounded by the active formatter — the
	// minimum already covers the worst-case glyph count.
	if ageMin > ageContent {
		ageContent = ageMin
	}

	specs := make([]format.Column, 0, 6+len(p.shownCols))
	if p.ShowTenantColumn(len(p.byTenant)) {
		specs = append(specs, format.Column{Min: tenantMin, Content: max(tenantMin, tenantContent), Weight: 0})
	}
	specs = append(specs,
		format.Column{Min: sevMin, Content: max(sevMin, sevContent), Weight: 0},
		// ALERTNAME is the unbounded flex column. Min is the floor
		// for narrow terminals; Content is set to format.FlexUnbounded so
		// the allocator never caps it, handing the column every
		// leftover cell on a wide terminal — even when every
		// alertname in view is short. Capping at the live max would
		// leave dead space the user could otherwise spend on the
		// labels they're scanning.
		format.Column{Min: alertNameMin, Content: format.FlexUnbounded, Weight: 1},
	)
	specs = append(specs, p.labelColumnSpecs()...)
	specs = append(specs,
		format.Column{Min: countMin, Content: max(countMin, countContent), Weight: 0},
		// STATE: cap the requested width so a wide 3-bucket breakdown
		// can't starve ALERTNAME. The renderer ellipsizes the breakdown
		// to the allocated width when it falls short of the measured
		// content (see padColumns / the STATE branch in renderRows).
		format.Column{Min: stateMin, Content: min(stateContentCap, max(stateMin, stateContent)), Weight: 0},
		format.Column{Min: ageMin, Content: ageContent, Weight: 0},
	)
	return specs
}

// labelColumnWidthFloor is the narrowest a measured label column
// gets. Below this a value is an ellipsis and a character or two,
// which says less than an empty cell would.
const labelColumnWidthFloor = 6

// measureLabelColumns measures each user-declared column over the
// whole filtered view, so a vertical scroll never shifts a width. A
// column with a configured Width needs no measuring and is left at
// zero, because labelColumnSpecs pins it before it reads this slice.
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
		content := labelcol.HeaderWidth(c)
		for j := range p.groups {
			if w := lipgloss.Width(labelCellAt(&p.groups[j], c.Index)); w > content {
				content = w
			}
		}
		out[i] = content
	}
	return out
}

// labelColumnSpecs turns the measured widths into allocator columns.
// A measured column flexes rather than reserving its full width:
// label values run long (a pod name, an instance URL), and a weight-0
// request that wide pushes the allocator into its proportional
// shrink, which takes the built-in columns below their own floors.
// Flexing reserves only the floor and grows into what is left
// alongside ALERTNAME, so a long value ellipsizes instead of
// collapsing the row. A column with a configured width is pinned
// there, because that is what the operator asked for.
func (p *Page) labelColumnSpecs() []format.Column {
	out := make([]format.Column, 0, len(p.shownCols))
	for i, c := range p.shownCols {
		if c.Width > 0 {
			out = append(out, format.Column{Min: c.Width, Content: c.Width, Weight: 0})
			continue
		}
		w := 0
		if i < len(p.labelWidths) {
			w = p.labelWidths[i]
		}
		out = append(out, format.Column{Min: min(labelColumnWidthFloor, w), Content: w, Weight: 1})
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
// renderer produces, so columnSpecs measures the true cell width.
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
// overflows it. The allocated width is capped in columnSpecs
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
	if ctx.stateIdx >= 0 && ctx.stateIdx < len(ctx.cols) {
		plain := stateBreakdownPlain(g, p.stateFormat)
		if w := ctx.cols[ctx.stateIdx]; lipgloss.Width(plain) > w {
			// Left plain on purpose: padColumns paints the cut text,
			// so a span past the ellipsis is dropped rather than moved.
			return format.Ellipsize(plain, w)
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

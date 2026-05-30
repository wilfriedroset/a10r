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
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	band := p.RenderErrorBand(p.now(), width, p.styles.Severity.Critical.GetForeground())
	bandLines := 0
	if band != "" {
		bandLines = 1
	}
	p.SetViewport(height-1-bandLines, len(p.groups))
	if len(p.groups) == 0 {
		// Render bg-less so the empty pane keeps the terminal
		// default background that the populated frame uses.
		body := p.emptyState()
		if band != "" {
			body = band + "\n" + body
		}
		return listpage.Pane(width, height, body)
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1-bandLines)
	body := headerLine + "\n" + rows
	if band != "" {
		body = band + "\n" + body
	}
	return listpage.Wrap(width, body)
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

func (p *Page) renderHeader(width int) string {
	cols := []string{sortKeySeverity, sortKeyName, sortKeyCount, sortKeyState, sortKeyAge}
	widths := p.columnWidths(width)
	// fg-only renderers so the header keeps the terminal default
	// background — painting palette bg inside the unstyled body
	// frame creates a coloured stripe (see feedback memory on
	// chrome rendering).
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", format.RowPrefixCols))
	idx := 0
	if p.ShowTenantColumn(len(p.byTenant)) && idx < len(widths) {
		b.WriteString(headerFg.Render(format.PadRight("TENANT", widths[idx])))
		idx++
	}
	for _, k := range cols {
		if idx >= len(widths) {
			break
		}
		if idx > 0 {
			b.WriteString(colSep)
		}
		label := strings.ToUpper(k)
		if arrow := p.sorter.ArrowFor(k); arrow != "" {
			label = label + " " + arrow
		}
		padded := format.PadRight(label, widths[idx])
		// Active column gets HeaderActive; the rest get the regular
		// Header foreground. The two tints plus the arrow glyph give
		// two distinct cues for "which sort is live" — one for the
		// eye scanning columns, one for the eye reading the arrow.
		if p.sorter.IsActive(k) {
			b.WriteString(activeFg.Render(padded))
		} else {
			b.WriteString(headerFg.Render(padded))
		}
		idx++
	}
	return b.String()
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
	cols := p.columnWidths(width)
	// STATE sits second-to-last in the rendered row; its allocated
	// width caps the breakdown so an over-cap breakdown ellipsizes
	// here rather than starving ALERTNAME (the cap lives in
	// columnSpecs). -1 (no STATE column visible) disables ellipsis.
	stateIdx := -1
	if len(cols) >= 2 {
		stateIdx = len(cols) - 2
	}
	var b strings.Builder
	// Reserve enough capacity for the visible page (rows × width)
	// plus per-row styling overhead so the Builder doesn't realloc
	// while every row appends. Multiplying by 2 covers the SGR
	// bytes lipgloss.Render injects per cell on coloured rows.
	b.Grow((end - p.TopRow()) * width * 2)
	for i := p.TopRow(); i < end; i++ {
		b.WriteString(p.renderRow(i, p.groups[i], cols, stateIdx, width, showTenant))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
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
func (p *Page) renderRow(i int, g alertGroup, cols []int, stateIdx, width int, showTenant bool) string {
	ageLabel := p.formatTime(g.oldestStart)
	if ageLabel == "" {
		ageLabel = "—"
	}
	_, marked := p.marks[g.key()]
	mark := " "
	if marked {
		mark = "✓"
	}
	rowStyled := i == p.Index() || marked || g.allSuppressed()
	sevCell := severityLabelForRank(g.severityRank)
	stateCell := p.stateCell(g, stateIdx, cols, rowStyled)
	if !rowStyled {
		sevCell = severityStyleForRank(g.severityRank, p.styles).Render(sevCell)
	}
	row := make([]string, 0, 6)
	if showTenant {
		row = append(row, g.tenant)
	}
	row = append(row,
		sevCell,
		alertNameCell(g),
		countCell(g),
		stateCell,
		ageLabel,
	)
	prefix := "  "
	if i == p.Index() {
		prefix = "▸ "
	}
	line := format.PadRight(prefix+mark+" "+p.padColumns(row, cols), width)
	switch {
	case i == p.Index():
		// k9s parity: cursor bg tracks the row's semantic colour
		// (max severity), not a static cursor colour.
		rowColor := severityStyleForRank(g.severityRank, p.styles).GetForeground()
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
// scope spans multiple backends and parts has 5 entries instead
// of 4. The alertname column is the flex slot: when its assigned
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
func (p *Page) padColumns(parts []string, cols []int) string {
	flexIdx := p.flexColumnIndex()
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		if i > 0 {
			b.WriteString(colSep)
		}
		if i == flexIdx {
			b.WriteString(format.PadRight(format.Ellipsize(v, cols[i]), cols[i]))
			continue
		}
		b.WriteString(format.PadRight(v, cols[i]))
	}
	return b.String()
}

// flexColumnIndex returns the position of the alertname column in
// the rendered row. When the TENANT column is hidden the flex
// column sits at index 1 (after SEVERITY); when shown, at index 2.
// Centralised so padColumns and any future per-cell styler agree
// on which column is the unbounded one.
func (p *Page) flexColumnIndex() int {
	if p.ShowTenantColumn(len(p.byTenant)) {
		return 2
	}
	return 1
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
func (p *Page) columnWidths(width int) []int {
	specs := p.columnSpecs()
	// Subtract the row prefix from total before distributing — the
	// allocator's contract is "fits in N cells", not "fits in N
	// minus chrome". Centralising the chrome subtraction here keeps
	// the spec construction pure and easy to test.
	budget := max(0, width-format.RowPrefixCols)
	return format.Distribute(specs, budget, len(colSep))
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
		if w := lipgloss.Width(severityLabelForRank(g.severityRank)); w > sevContent {
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

	specs := make([]format.Column, 0, 6)
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

// formatTime renders ts according to the page's active time
// format. Mirrors the silences / alert-detail formatters so the
// three views agree on how the toggle reads.
func (p *Page) formatTime(ts time.Time) string {
	return timerender.Display(p.timeFormat, p.now(), ts)
}

// severityLabelForRank is the inverse of backend.SeverityRank: it
// maps the group's max rank back to its printable label so the
// SEVERITY cell headlines the worst severity in the group. Rank 0
// (no recognised severity anywhere in the group) renders "—".
func severityLabelForRank(rank int) string {
	switch rank {
	case 3:
		return "critical"
	case 2:
		return "warning"
	case 1:
		return "info"
	}
	return "—"
}

// severityStyleForRank returns the lipgloss style for the group's max
// severity rank so the renderer can foreground-tint the SEVERITY cell.
// Rank 0 falls back to Severity.Unknown so every cell gets a
// consistent palette ref rather than a bare default.
func severityStyleForRank(rank int, styles *theme.Styles) lipgloss.Style {
	switch rank {
	case 3:
		return styles.Severity.Critical
	case 2:
		return styles.Severity.Warning
	case 1:
		return styles.Severity.Info
	}
	return styles.Severity.Unknown
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
func (p *Page) stateCell(g alertGroup, stateIdx int, cols []int, rowStyled bool) string {
	if stateIdx >= 0 && stateIdx < len(cols) {
		plain := stateBreakdownPlain(g, p.stateFormat)
		if w := cols[stateIdx]; lipgloss.Width(plain) > w {
			return format.Ellipsize(plain, w)
		}
	}
	return renderStateBreakdown(g, p.stateFormat, p.styles, rowStyled)
}

// renderStateBreakdown renders the STATE cell's per-state tally: non-
// zero buckets only, fixed active → suppressed → unprocessed order,
// summing to count. On plain rows each token is foreground-tinted by
// state; on cursor / marked / all-suppressed rows the per-token colour
// is skipped (rowStyled=true) so the row-level style wins.
func renderStateBreakdown(g alertGroup, f stateformat.Format, styles *theme.Styles, rowStyled bool) string {
	buckets := orderedBuckets(g)
	parts := make([]string, 0, len(buckets))
	for _, b := range buckets {
		tok := stateToken(b.count, b.state, f)
		if !rowStyled {
			tok = stateTokenStyle(b.state, styles).Render(tok)
		}
		parts = append(parts, tok)
	}
	return strings.Join(parts, stateBreakdownSep(f))
}

// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	band := p.renderErrorBand(width)
	bandLines := 0
	if band != "" {
		bandLines = 1
	}
	p.BodyHeight = height - 1 - bandLines // header + optional error band; rest is row budget
	if len(p.view) == 0 {
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		body := p.emptyState()
		if band != "" {
			body = band + "\n" + body
		}
		return lipgloss.NewStyle().Width(width).Height(height).Render(body)
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1-bandLines)
	body := headerLine + "\n" + rows
	if band != "" {
		body = band + "\n" + body
	}
	return lipgloss.NewStyle().Width(width).Render(body)
}

// renderErrorBand returns a one-line styled error message for the
// View to prepend, or "" when no in-scope tenant is reporting an
// error. The band is fg-tinted via theme.Body.Default with the
// severity-error palette so it reads as a warning without
// painting a background that would clash with the panel chrome.
//
// Truncation: the band fits exactly width columns. Long upstream
// errors (e.g. nested transport-error chains) are clipped to keep
// the page layout stable.
func (p *Page) renderErrorBand(width int) string {
	msg := p.ErrorBand()
	if msg == "" {
		return ""
	}
	prefix := "! "
	full := prefix + msg
	if lipgloss.Width(full) > width {
		full = format.SGRTruncate(full, width)
	}
	// Theme: reuse the severity-critical fg so the band is loud but
	// stays fg-only (no painted background — see feedback memory
	// on chrome rendering).
	style := lipgloss.NewStyle().Foreground(p.styles.Severity.Critical.GetForeground())
	return style.Render(full)
}

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.Filter != "" || p.stateFilter != "" {
		return "no alerts match the active filter — Esc clears the prompt, Shift+F cycles state filters"
	}
	if p.totalAlerts() == 0 {
		return "no alerts (yet) — the poller will refresh on the next tick"
	}
	return "no alerts in view"
}

// renderHeader returns the column-title row with a sort marker
// on the active column. Titles are upper-cased and styled via
// theme.Table.Header (k9s-style yellow on base in catppuccin) so
// they stand apart from the data rows. A leading TENANT column
// appears when the active scope spans multiple backends.
func (p *Page) renderHeader(width int) string {
	cols := []string{sortKeySeverity, sortKeyName, sortKeyState, sortKeyAge}
	widths := p.columnWidths(width)
	// fg-only renderers so the header keeps the terminal default
	// background — painting palette bg inside the unstyled body
	// frame creates a coloured stripe (see feedback memory on
	// chrome rendering).
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", rowPrefixCols))
	idx := 0
	if p.showTenantColumn() && idx < len(widths) {
		b.WriteString(headerFg.Render(format.PadRight("TENANT", widths[idx])))
		idx++
	}
	for _, k := range cols {
		if idx >= len(widths) {
			break
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
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	p.recomputeScroll()
	end := min(p.TopRow+maxRows, len(p.view))

	showTenant := p.showTenantColumn()
	// Compute column widths once per frame: the spec builder walks
	// the full view to measure max content widths, and re-running
	// it per row would turn the render into O(rows²) under a
	// storm. The header renderer makes its own call (one per
	// frame, not per row) so the cost lands once on the outer loop
	// either way.
	cols := p.columnWidths(width)
	var b strings.Builder
	// Reserve enough capacity for the visible page (rows × width)
	// plus per-row styling overhead so the Builder doesn't realloc
	// while every row appends. Multiplying by 2 covers the SGR
	// bytes lipgloss.Render injects per cell on coloured rows.
	b.Grow((end - p.TopRow) * width * 2)
	for i := p.TopRow; i < end; i++ {
		entry := p.view[i]
		a := entry.a
		ageLabel := p.formatTime(a.StartsAt)
		if ageLabel == "" {
			ageLabel = "—"
		}
		_, marked := p.marks[a.Fingerprint]
		mark := " "
		if marked {
			mark = "✓"
		}
		// Per-cell severity colour applies only to plain rows.
		// Cursor / marked / suppressed rows wrap the entire line in
		// a row-level style; nested ANSI inside that wrap is fragile
		// across terminals, and per Q1.2 the row-level style is
		// supposed to win — so skip the cell-level colour entirely
		// for those three cases.
		rowStyled := i == p.Cursor || marked || a.State == backend.AlertStateSuppressed
		sevCell := severityOf(a)
		if !rowStyled {
			sevCell = severityStyle(a, p.styles).Render(sevCell)
		}
		row := make([]string, 0, 5)
		if showTenant {
			row = append(row, entry.tenant)
		}
		row = append(row,
			sevCell,
			a.Labels["alertname"],
			string(a.State),
			ageLabel,
		)
		prefix := "  "
		if i == p.Cursor {
			prefix = "▸ "
		}
		// Pad to the full width before styling. Precedence:
		// cursor > marked > dimmed. Cursor wraps the whole row in
		// fg+bg (the salient "you are here" signal); Marked and
		// Dimmed both change the foreground only so the row keeps
		// the body's default background — k9s "tinted text"
		// rather than two competing highlighted stripes stacked on
		// top of each other. Dimmed fires when the alert is
		// suppressed (silenced / inhibited / muted by a time
		// interval) and is neither cursor nor marked — same
		// treatment k9s gives "Completed" pods. Marked beats
		// dimmed on purpose: marked is an explicit user action,
		// suppression is ambient state.
		line := format.PadRight(prefix+mark+" "+p.padColumns(row, cols), width)
		switch {
		case i == p.Cursor:
			// k9s parity: cursor bg tracks the row's semantic
			// colour (severity), not the static cursorBgColor.
			// `select_table.go:128` in k9s replaces the selected
			// style on every selection-changed event; this is the
			// equivalent.
			rowColor := severityStyle(a, p.styles).GetForeground()
			line = p.styles.Table.CursorOver(rowColor).Render(line)
		case marked:
			line = p.styles.Table.MarkedFg.Render(line)
		case a.State == backend.AlertStateSuppressed:
			line = p.styles.Table.DimmedFg.Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// rowPrefixCols is the space the rendered row reserves for its
// leading "[cursor] [mark] " prefix (▸ or space + mark glyph or
// space + separator). renderHeader prepends the same number of
// spaces so the column titles line up with the data columns.
const rowPrefixCols = 4

// flexUnbounded is the Content sentinel for the alertname column
// in columnSpecs. Using a finite-but-huge value (rather than
// math.MaxInt) keeps the allocator's integer math honest while
// guaranteeing no realistic terminal width can reach the cap —
// 1 << 16 covers a 65k-cell-wide terminal, well past any current
// hardware. Picked over MaxInt to avoid edge cases in the
// proportional-shrink path multiplying widths by total budget.
const flexUnbounded = 1 << 16

// padColumns lays out the row's columns at pre-computed cols
// widths. The leading TENANT column is optional — added when
// scope spans multiple backends and parts has 5 entries instead
// of 4. The alertname column is the flex slot: when its assigned
// width is narrower than the label, the cell is ellipsized with
// format.EllipsizeSuffix so the truncation reads as intentional
// ("…") rather than as a silent slice. Other columns fall back to
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
	if p.showTenantColumn() {
		return 2
	}
	return 1
}

// columnWidths returns the per-column widths (TENANT optional,
// then SEVERITY, ALERTNAME flex, STATE, AGE) by measuring the
// active dataset and handing the result to the duf-style
// distributor in package format. ALERTNAME is the unbounded
// (weight=1) flex column; the rest are weight=0 fixed columns
// that never grow past max(min, content). Per-row content widths
// come from the filtered+sorted view so the layout reacts to the
// data the user is actually looking at — long alertnames trigger
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
	budget := max(0, width-rowPrefixCols)
	return format.Distribute(specs, budget, 0)
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
		sevMin   = 12
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
	if p.timeFormat == app.TimeFormatAbsolute {
		ageMin = ageAbsMin
	}

	// Measure max content width per column from the live dataset.
	// Header labels are included so a column never collapses under
	// its own title. ALERTNAME is intentionally absent — its
	// Content is the flexUnbounded sentinel, so the per-row max
	// would never beat the cap and walking it every frame is dead
	// work for nothing.
	var (
		tenantContent = lipgloss.Width("TENANT")
		sevContent    = lipgloss.Width("SEVERITY")
		stateContent  = lipgloss.Width("STATE")
		ageContent    = lipgloss.Width("AGE")
	)
	for _, e := range p.view {
		if w := lipgloss.Width(e.tenant); w > tenantContent {
			tenantContent = w
		}
		if w := lipgloss.Width(severityOf(e.a)); w > sevContent {
			sevContent = w
		}
		if w := lipgloss.Width(string(e.a.State)); w > stateContent {
			stateContent = w
		}
	}
	// AGE content width is bounded by the active formatter — the
	// minimum already covers the worst-case glyph count.
	if ageMin > ageContent {
		ageContent = ageMin
	}

	specs := make([]format.Column, 0, 5)
	if p.showTenantColumn() {
		specs = append(specs, format.Column{Min: tenantMin, Content: max(tenantMin, tenantContent), Weight: 0})
	}
	specs = append(specs,
		format.Column{Min: sevMin, Content: max(sevMin, sevContent), Weight: 0},
		// ALERTNAME is the unbounded flex column. Min is the floor
		// for narrow terminals; Content is set to flexUnbounded so
		// the allocator never caps it, handing the column every
		// leftover cell on a wide terminal — even when every
		// alertname in view is short. Capping at the live max would
		// leave dead space the user could otherwise spend on the
		// labels they're scanning.
		format.Column{Min: alertNameMin, Content: flexUnbounded, Weight: 1},
		format.Column{Min: stateMin, Content: max(stateMin, stateContent), Weight: 0},
		format.Column{Min: ageMin, Content: ageContent, Weight: 0},
	)
	return specs
}

// formatTime renders ts according to the page's active time
// format. Mirrors the silences / alert-detail formatters so the
// three views agree on how the toggle reads.
func (p *Page) formatTime(ts time.Time) string {
	if p.timeFormat == app.TimeFormatAbsolute {
		return header.FormatAbsolute(ts)
	}
	return header.FormatRelative(p.now(), ts)
}

// severityOf returns the printable severity label, falling back
// to "—" when no severity label is set.
func severityOf(a backend.Alert) string {
	if v, ok := a.Labels["severity"]; ok && v != "" {
		return v
	}
	return "—"
}

// severityStyle returns the lipgloss style for a's severity label so
// the renderer can foreground-tint the SEVERITY cell. Falls back to
// Severity.Unknown for missing / unrecognised values so every cell
// gets a consistent palette ref rather than a bare default.
func severityStyle(a backend.Alert, styles *theme.Styles) lipgloss.Style {
	switch strings.ToLower(a.Labels["severity"]) {
	case "critical":
		return styles.Severity.Critical
	case "warning":
		return styles.Severity.Warning
	case "info":
		return styles.Severity.Info
	}
	return styles.Severity.Unknown
}

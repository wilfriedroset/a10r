// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	p.bodyHeight = height - 1 // header takes one line; the rest is table-row budget
	if len(p.view) == 0 {
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		return lipgloss.NewStyle().Width(width).Height(height).Render(p.emptyState())
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1)
	body := headerLine + "\n" + rows
	return lipgloss.NewStyle().Width(width).Render(body)
}

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.filter != "" || p.stateFilter != "" {
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
	headerFg := theme.FgOnly(p.styles.Table.Header.GetForeground())
	activeFg := theme.FgOnly(p.styles.Table.HeaderActive.GetForeground())

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
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, maxRows, len(p.view))
	end := min(p.topRow+maxRows, len(p.view))

	showTenant := p.showTenantColumn()
	var b strings.Builder
	for i := p.topRow; i < end; i++ {
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
		rowStyled := i == p.cursor || marked || a.State == backend.AlertStateSuppressed
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
		if i == p.cursor {
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
		line := format.PadRight(prefix+mark+" "+p.padColumns(row, width), width)
		switch {
		case i == p.cursor:
			// k9s parity: cursor bg tracks the row's semantic
			// colour (severity), not the static cursorBgColor.
			// `select_table.go:128` in k9s replaces the selected
			// style on every selection-changed event; this is the
			// equivalent.
			rowColor := severityStyle(a, p.styles).GetForeground()
			line = p.styles.Table.CursorOver(rowColor).Render(line)
		case marked:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		case a.State == backend.AlertStateSuppressed:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Dimmed.GetForeground()).
				Render(line)
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

// padColumns lays out the row's columns at fixed widths with one
// flex column for the alertname. The leading TENANT column is
// optional — added when scope spans multiple backends and parts
// has 5 entries instead of 4. AGE is widened in absolute mode so
// the ISO local timestamp ("2026-05-01 13:45:00", 19 cols) fits
// without truncation per Q7.4.
func (p *Page) padColumns(parts []string, width int) string {
	cols := p.columnWidths(width)
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(format.PadRight(v, cols[i]))
	}
	return b.String()
}

// columnWidths returns the per-column widths (TENANT optional,
// then SEVERITY, ALERTNAME flex, STATE, AGE). Extracted so the
// header renderer can pad each label to its own column width
// before applying per-cell styling — padColumns concatenates the
// raw padded strings, but per-cell styling needs each cell's
// width separately.
func (p *Page) columnWidths(width int) []int {
	tenantCol := 0
	if p.showTenantColumn() {
		tenantCol = 16
	}
	const sevCol, stateCol = 12, 14
	ageCol := 12
	if p.timeFormat == app.TimeFormatAbsolute {
		ageCol = 20
	}
	flex := max(width-tenantCol-sevCol-stateCol-ageCol-rowPrefixCols, 10)

	cols := make([]int, 0, 5)
	if tenantCol > 0 {
		cols = append(cols, tenantCol)
	}
	cols = append(cols, sevCol, flex, stateCol, ageCol)
	return cols
}

// formatTime renders ts according to the page's active time
// format. Mirrors the silences / alert-detail formatters so the
// three views agree on how the toggle reads.
func (p *Page) formatTime(ts time.Time) string {
	if p.timeFormat == app.TimeFormatAbsolute {
		return header.FormatAbsolute(ts)
	}
	return header.FormatAge(p.now(), ts)
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
func severityStyle(a backend.Alert, styles theme.Styles) lipgloss.Style {
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

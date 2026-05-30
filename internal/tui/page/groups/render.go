// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// emptyState picks the right body for an empty list. The cold-
// start / refresh-in-flight loading hint lives in the title; the
// body stays empty in that window so there's no duplicate spinner.
// After the first DataMsg lands and there's genuinely nothing to
// show, the body returns an info hint that distinguishes "no
// groups at all" from "filter masked them all".
func (p *Page) emptyState() string {
	if p.SpinnerActive(p.ScopeIncludes) {
		return ""
	}
	if p.Filter != "" {
		return "no groups match the active filter — Esc clears the prompt"
	}
	return "no groups (yet)"
}

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	band := p.RenderErrorBand(p.now(), width, p.styles.Severity.Critical.GetForeground())
	bandLines := 0
	if band != "" {
		bandLines = 1
	}
	rows := p.rows()
	p.SetViewport(height-1-bandLines, len(rows))
	if len(rows) == 0 {
		// Render bg-less so the empty pane keeps the terminal
		// default background that the populated frame uses.
		body := p.emptyState()
		if band != "" {
			body = band + "\n" + body
		}
		return listpage.Pane(width, height, body)
	}
	maxRows := min(height-1-bandLines, len(rows))
	end := min(p.TopRow()+maxRows, len(rows))
	out := make([]string, 0, end-p.TopRow()+2)
	if band != "" {
		out = append(out, band)
	}
	out = append(out, p.renderHeader(width))
	for i := p.TopRow(); i < end; i++ {
		r := rows[i]
		out = append(out, p.renderRow(r, i == p.Index(), width))
	}
	return listpage.Wrap(width, strings.Join(out, "\n"))
}

// renderHeader emits the column-title row. NAME / COUNT /
// SEVERITY sit above their respective data columns; the active
// axis carries an `↑` (ASC) or `↓` (DESC) arrow per the alerts /
// silences convention. A leading TENANT slot appears when scope
// spans multiple in-scope backends.
func (p *Page) renderHeader(width int) string {
	tenantW, nameW, countW, sevW := p.columnWidths(width)
	// fg-only so the header keeps the terminal default background
	// — painted palette bg in the unstyled body frame creates a
	// coloured stripe.
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg

	render := func(label, key string, w int) string {
		if arrow := p.sorter.ArrowFor(key); arrow != "" {
			label = label + " " + arrow
		}
		if p.sorter.IsActive(key) {
			return activeFg.Render(format.PadRight(label, w))
		}
		return headerFg.Render(format.PadRight(label, w))
	}

	var b strings.Builder
	if tenantW > 0 {
		b.WriteString(headerFg.Render(format.PadRight("TENANT", tenantW)))
	}
	// Tree-marker column has no header label — it carries ▸/▾ on
	// data rows only.
	b.WriteString(strings.Repeat(" ", treeColWidth))
	b.WriteString(render("NAME", sortKeyName, nameW))
	b.WriteString(render("COUNT", sortKeyCount, countW))
	b.WriteString(render("SEVERITY", sortKeySeverity, sevW))
	return format.PadRight(b.String(), width)
}

// renderRow renders one row of the tree — group header (alertIdx
// == -1) or leaf — into the body, dispatching the cell content to
// writeGroupCells / writeLeafCells and wrapping the cursor row in
// the row's semantic colour.
func (p *Page) renderRow(r row, focused bool, width int) string {
	entry := p.flat[r.groupIdx]
	tenantW, nameW, countW, sevW := p.columnWidths(width)

	var b strings.Builder
	b.Grow(width + 64)
	// Leading TENANT slot: present on every row when the scope
	// spans multiple tenants so columns line up regardless of
	// row kind. Group rows fill it; leaf rows leave it blank
	// since the parent header already names the source backend.
	if tenantW > 0 {
		if r.alertIdx == -1 {
			b.WriteString(format.PadRight(entry.tenant, tenantW))
		} else {
			b.WriteString(strings.Repeat(" ", tenantW))
		}
	}

	if r.alertIdx == -1 {
		p.writeGroupCells(&b, r, entry, focused, nameW, countW, sevW)
	} else {
		p.writeLeafCells(&b, entry, r.alertIdx, focused, nameW, countW, sevW)
	}

	body := format.PadRight(b.String(), width)
	if focused {
		// k9s parity: cursor bg tracks the row's semantic colour
		// (max severity for groups) rather than the static
		// cursorBgColor — see k9s select_table.go:128.
		rowColor := severityStyleByRank(entry.severityRank, p.styles).GetForeground()
		return p.styles.Table.CursorOver(rowColor).Render(body)
	}
	return body
}

// writeGroupCells renders a group-header row's NAME/COUNT/SEVERITY
// cells. On the cursor row per-cell colouring is skipped because the
// whole line is wrapped in fg+bg downstream and nested ANSI inside
// that wrap is fragile.
func (p *Page) writeGroupCells(b *strings.Builder, r row, entry groupEntry, focused bool, nameW, countW, sevW int) {
	marker := "▸"
	if p.expanded[r.groupIdx] {
		marker = "▾"
	}
	b.WriteString(marker + " ")

	summary := labelSummary(entry.g.Labels)
	if !focused {
		summary = styledLabelSummary(entry.g.Labels, p.styles)
	}
	b.WriteString(format.PadRight(summary, nameW))

	count := strconv.Itoa(len(entry.g.Alerts))
	b.WriteString(format.PadRight(count, countW))

	sev := severityLabelByRank(entry.severityRank)
	if !focused {
		sev = severityStyleByRank(entry.severityRank, p.styles).Render(sev)
	}
	b.WriteString(format.PadRight(sev, sevW))
}

// writeLeafCells renders a leaf row: the labels distinguishing this
// leaf from its siblings (or the alertname when there are none, so
// the row never reads blank) plus state, collapsed into one wide
// cell. A leaf has no group-level count and its severity rolls into
// the parent's SEVERITY cell, so per-leaf alignment there would be
// padding around dead air — per-alert detail lives behind Enter.
func (p *Page) writeLeafCells(b *strings.Builder, entry groupEntry, alertIdx int, focused bool, nameW, countW, sevW int) {
	b.WriteString(strings.Repeat(" ", treeColWidth))
	a := entry.g.Alerts[alertIdx]
	diff := backend.DistinguishingLabels(a, entry.common)
	var labelText string
	if len(diff) > 0 {
		if focused {
			labelText = labelSummary(diff)
		} else {
			labelText = styledLabelSummary(diff, p.styles)
		}
	} else {
		labelText = a.Labels["alertname"]
		if !focused {
			labelText = p.styles.YAML.Key.Render(labelText)
		}
	}
	state := string(a.State)
	if !focused {
		state = p.styles.YAML.Value.Render(state)
	}
	leaf := "  " + labelText + " — " + state
	b.WriteString(format.PadRight(leaf, nameW+countW+sevW))
}

// Column geometry. tenantColWidth mirrors the alerts / silences
// pages so the three views align across page switches.
// treeColWidth reserves space for the ▸/▾ marker plus a trailing
// space; countColWidth fits up to a 9-digit count, severityColWidth
// fits the longest severity label ("critical") with breathing room.
const (
	tenantColWidth   = 16
	treeColWidth     = 2
	countColWidth    = 10
	severityColWidth = 12
)

// columnWidths returns the rendered widths for the optional TENANT
// column, then NAME (flex), COUNT, and SEVERITY. NAME absorbs the
// remainder so the layout fills the body width without truncating
// the fixed cells. Floored at 10 so a narrow terminal still leaves
// the labels readable rather than collapsing the NAME column to
// zero.
func (p *Page) columnWidths(width int) (tenant, name, count, sev int) {
	if p.ShowTenantColumn(len(p.byTenant)) {
		tenant = tenantColWidth
	}
	count = countColWidth
	sev = severityColWidth
	name = max(width-tenant-treeColWidth-count-sev, 10)
	return tenant, name, count, sev
}

// severityLabelByRank inverts backend.SeverityRank to the printable
// label so the SEVERITY column reads consistently with the alerts
// page's per-row severity cell. Unknown rank renders as `—` to
// match alerts.severityOf.
func severityLabelByRank(rank int) string {
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

// severityStyleByRank picks the lipgloss style for the SEVERITY
// cell, mirroring alerts.severityStyle so a "critical" cell tints
// the same on both pages.
func severityStyleByRank(rank int, styles *theme.Styles) lipgloss.Style {
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

// labelSummary renders a "k=v, k=v" preview of a label-set so the
// group header is identifiable at a glance. Plain-text variant
// kept for filter matching (lower-cased substring search needs
// the unstyled string) and as the cursor-row body where the
// row-level fg+bg wrap supersedes per-cell colouring.
func labelSummary(labels map[string]string) string {
	keys := sortedLabelKeys(labels)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + labels[k]
	}
	return strings.Join(parts, ",")
}

// styledLabelSummary returns the same `k=v, k=v` preview with the
// label name rendered in theme.YAML.Key and the value in
// theme.YAML.Value — matches the YAML viewer's colouring so the
// k=v pair reads consistently across the TUI. Punctuation (= and
// ,) uses theme.YAML.Punct so the visual hierarchy is name >
// value > separator.
func styledLabelSummary(labels map[string]string, styles *theme.Styles) string {
	keys := sortedLabelKeys(labels)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = styles.YAML.Key.Render(k) +
			styles.YAML.Punct.Render("=") +
			styles.YAML.Value.Render(labels[k])
	}
	return strings.Join(parts, styles.YAML.Punct.Render(","))
}

// sortedLabelKeys returns the keys of labels in deterministic
// alphabetical order. Pulled out so labelSummary and
// styledLabelSummary share the ordering rule — diverging
// orderings would make the styled vs plain output disagree on
// how a group reads.
func sortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

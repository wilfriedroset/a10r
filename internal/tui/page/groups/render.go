// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"maps"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// emptyState picks the right body for an empty list. The cold-
// start / refresh-in-flight loading hint lives in the title; the
// body stays empty in that window so there's no duplicate spinner.
// After the first DataMsg lands and there's genuinely nothing to
// show, the body returns an info hint that distinguishes "no
// groups at all" from "filter masked them all".
func (p *Page) emptyState() string {
	if !p.polled() || p.Refreshing {
		return ""
	}
	if p.Filter != "" {
		return "no groups match the active filter — Esc clears the prompt"
	}
	return "no groups (yet)"
}

// distinguishingLabels returns the labels in a that aren't shared
// across every sibling — i.e. the keys whose value diverges from
// the group's commonLabels intersection. Renders on leaf rows so
// each leaf identifies the actual instance (instance / pod /
// host / …) rather than echoing the labels already painted in the
// group header.
func distinguishingLabels(a backend.Alert, common map[string]string) map[string]string {
	out := make(map[string]string, len(a.Labels))
	for k, v := range a.Labels {
		if cv, ok := common[k]; ok && cv == v {
			continue
		}
		out[k] = v
	}
	return out
}

// commonLabels returns the labels that appear with the same value
// in every alert. Used by the group-silence flow so the silence
// form opens with matchers covering exactly the alerts in this
// group.
func commonLabels(alerts []backend.Alert) map[string]string {
	if len(alerts) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(alerts[0].Labels))
	maps.Copy(out, alerts[0].Labels)
	for _, a := range alerts[1:] {
		for k, v := range out {
			other, ok := a.Labels[k]
			if !ok || other != v {
				delete(out, k)
			}
		}
	}
	return out
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
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
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
// == -1) or leaf — into the body. The deep nesting is pre-existing
// and out of scope for the structural file split; refactoring it
// is its own follow-up.
//
//nolint:nestif // pre-existing complexity in the group/leaf branch.
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
		marker := "▸"
		if p.expanded[r.groupIdx] {
			marker = "▾"
		}
		b.WriteString(marker + " ")

		summary := labelSummary(entry.g.Labels)
		if !focused {
			// Cursor row wraps the whole line in fg+bg (alerts page
			// convention); nested ANSI inside the wrap is fragile, so
			// per-cell colouring is skipped on the cursor row.
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
	} else {
		// Leaf row: empty tree slot, then the labels that
		// distinguish this leaf from its siblings (instance, pod,
		// host, …) plus its state. The group header already shows
		// the labels common to every alert; echoing them on every
		// leaf is dead pixels and hides the field that actually
		// identifies the instance. Falls back to the alertname
		// when distinguishing labels are empty (true duplicates)
		// so the row never reads as blank.
		b.WriteString(strings.Repeat(" ", treeColWidth))
		a := entry.g.Alerts[r.alertIdx]
		diff := distinguishingLabels(a, entry.common)
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
		// Leaves collapse the NAME / COUNT / SEVERITY columns into
		// one wide cell on purpose: a leaf has no group-level count
		// and its severity is already rolled into the parent's
		// SEVERITY cell, so per-leaf alignment under those columns
		// would be padding around dead air. Drilling via Enter is
		// where per-alert detail belongs.
		leaf := "  " + labelText + " — " + state
		b.WriteString(format.PadRight(leaf, nameW+countW+sevW))
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

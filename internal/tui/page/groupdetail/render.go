// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"slices"
	"sort"
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
	strip := p.renderCommonStrip()
	stripLines := 0
	if strip != "" {
		stripLines = 1
	}
	// One row each for the strip (when shown), error band (when
	// present), and the column-title header; the rest is data rows.
	p.SetViewport(height-1-bandLines-stripLines, len(p.view))
	if len(p.view) == 0 {
		body := p.emptyState()
		if strip != "" {
			body = strip + "\n" + body
		}
		if band != "" {
			body = band + "\n" + body
		}
		return listpage.Pane(width, height, body)
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1-bandLines-stripLines)
	body := headerLine + "\n" + rows
	if strip != "" {
		body = strip + "\n" + body
	}
	if band != "" {
		body = band + "\n" + body
	}
	return listpage.Wrap(width, body)
}

// emptyState differentiates "alert resolved while viewing" (no
// instances remain) from "filter hides everything". The resolved
// case does NOT auto-pop — the operator keeps the page and Esc goes
// back when they choose.
func (p *Page) emptyState() string {
	if len(p.instances) == 0 {
		return "no instances — alert resolved (Esc to go back)"
	}
	if p.Filter != "" || p.stateFilter != "" {
		return "no instances match the active filter — Esc clears the prompt, Shift+F cycles state filters"
	}
	return "no instances in view"
}

// renderCommonStrip returns the one-line `common: k=v · k=v` strip
// rendered above the table header by default. Empty when the strip is
// collapsed (Shift+C) or no common label remains worth showing — the
// caller then reclaims the row for the table. `alertname` is dropped
// because the title already carries it, so a group whose only shared
// label is its alertname renders no strip rather than an all-noise one.
func (p *Page) renderCommonStrip() string {
	if p.commonCollapsed {
		return ""
	}
	keys := make([]string, 0, len(p.common))
	for k := range p.common {
		if k == "alertname" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts,
			p.styles.YAML.Key.Render(k)+
				p.styles.YAML.Punct.Render("=")+
				p.styles.YAML.Value.Render(p.common[k]))
	}
	sep := p.styles.YAML.Punct.Render(" · ")
	return p.styles.YAML.Key.Render("common: ") + strings.Join(parts, sep)
}

// renderHeader returns the column-title row with a sort marker on the
// active column. SEVERITY, INSTANCE (flex), STATE, AGE — no TENANT,
// no COUNT.
func (p *Page) renderHeader(width int) string {
	cols := []string{sortKeySeverity, sortKeyInstance, sortKeyState, sortKeyAge}
	widths := p.columnWidths(width)
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", format.RowPrefixCols))
	for idx, k := range cols {
		if idx >= len(widths) {
			break
		}
		if idx > 0 {
			b.WriteString(colSep)
		}
		label := headerLabel(k)
		// STATE has no sort column; only the sortable columns get an
		// arrow / active tint.
		if arrow := p.sorter.ArrowFor(k); arrow != "" {
			label = label + " " + arrow
		}
		padded := format.PadRight(label, widths[idx])
		if p.sorter.IsActive(k) {
			b.WriteString(activeFg.Render(padded))
		} else {
			b.WriteString(headerFg.Render(padded))
		}
	}
	return b.String()
}

// sortKeyState labels the STATE column header. It is NOT a sort key —
// the column is non-sortable on this page — but the header renderer
// walks a uniform key list, so the label lives here alongside the
// real keys.
const sortKeyState = "state"

func headerLabel(k string) string {
	if k == sortKeyState {
		return "STATE"
	}
	return strings.ToUpper(k)
}

// renderRows returns the visible window of data rows, reconciling the
// scroll window against the cursor each frame.
//
// Colour follows instance state: a FIRING (active) instance that is
// neither the cursor (its row-level highlight wins) nor marked gets the
// full treatment — its SEVERITY cell tints and its distinguishing
// labels take the YAML palette, matching the groups page so a k=v pair
// reads consistently across the TUI. Suppressed and unprocessed
// instances recede: the whole row dims, so the firing ones the operator
// can still act on stand out. The cursor and marked rows keep their
// row-level wrap (nested ANSI inside it is fragile), so their labels
// stay plain under the wrap.
func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	end := min(p.TopRow()+maxRows, len(p.view))
	cols := p.columnWidths(width)
	flexW := 0
	if flexColumnIndex < len(cols) {
		flexW = cols[flexColumnIndex]
	}
	var b strings.Builder
	b.Grow((end - p.TopRow()) * width * 2)
	for i := p.TopRow(); i < end; i++ {
		b.WriteString(p.renderRow(i, cols, flexW, width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderRow renders one instance row at the pre-computed column
// widths. See renderRows for the colour-by-state contract.
func (p *Page) renderRow(i int, cols []int, flexW, width int) string {
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
	isCursor := i == p.Index()
	isActive := a.State == backend.AlertStateActive
	colour := isActive && !isCursor && !marked

	sevCell := severityOf(a)
	// Clip the distinguishing labels on the PLAIN string (so the
	// middle-out ellipsis and column widths stay correct), then colour
	// the result — colouring never changes the cell's width.
	labels := ellipsizeMiddle(entry.distinguishSummary, flexW)
	if colour {
		sevCell = severityStyle(a, p.styles).Render(sevCell)
		labels = p.styleDistinguish(labels)
	}
	prefix := "  "
	if isCursor {
		prefix = "▸ "
	}
	row := []string{sevCell, labels, stateToken(a.State, p.stateFormat), ageLabel}
	line := format.PadRight(prefix+mark+" "+p.padColumns(row, cols), width)
	switch {
	case isCursor:
		return p.styles.Table.CursorOver(severityStyle(a, p.styles).GetForeground()).Render(line)
	case marked:
		return p.styles.Table.MarkedFg.Render(line)
	case !isActive:
		return p.styles.Table.DimmedFg.Render(line)
	}
	return line
}

// flexColumnIndex is the position of the INSTANCE (distinguishing-
// labels) flex column in the rendered row: index 1, after SEVERITY.
// No TENANT column on this page, so it never shifts.
const flexColumnIndex = 1

// padColumns lays out the row at the pre-computed widths, joining
// adjacent cells with a single inter-column space (colSep) so columns
// never fuse. Cells arrive pre-clipped — renderRows middle-clips the
// flex distinguishing-labels cell (and optionally colours it) before
// calling — so this only pads each cell to its column width.
func (p *Page) padColumns(parts []string, cols []int) string {
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		if i > 0 {
			b.WriteString(colSep)
		}
		b.WriteString(format.PadRight(v, cols[i]))
	}
	return b.String()
}

// styleDistinguish colours an already-clipped distinguishing-labels
// cell with the YAML palette (name / `=` / value / separator), matching
// the groups page's k=v styling. It runs AFTER the middle-out clip on
// the plain string, so the ellipsis stays correct and colouring never
// changes the cell's width — layout is unaffected. A fragment the clip
// left without an `=` (a rare middle-cut artefact) renders in the value
// colour.
func (p *Page) styleDistinguish(clipped string) string {
	if clipped == "" {
		return clipped
	}
	pairs := strings.Split(clipped, " · ")
	for i, pair := range pairs {
		name, val, ok := strings.Cut(pair, "=")
		if !ok {
			pairs[i] = p.styles.YAML.Value.Render(pair)
			continue
		}
		pairs[i] = p.styles.YAML.Key.Render(name) +
			p.styles.YAML.Punct.Render("=") +
			p.styles.YAML.Value.Render(val)
	}
	return strings.Join(pairs, p.styles.YAML.Punct.Render(" · "))
}

// colSep is the single inter-column space the renderer inserts between
// adjacent cells. colSeparator is its width, passed to
// format.Distribute so the budget reserves n-1 gap cells.
const (
	colSep       = " "
	colSeparator = 1
)

// ellipsizeMiddle clips s to at most w terminal cells, replacing the
// middle with a single ellipsis so BOTH the head and the discriminating
// tail survive. Two instance values sharing a long prefix but differing
// in the tail (`…-1a-0042` vs `…-1b-0117`) stay distinguishable, where a
// tail-truncating ellipsis would collapse them to the same shared
// prefix. Returns "" for w <= 0 and s unchanged when it already fits;
// falls back to a tail ellipsis (format.Ellipsize) at w == 1 where no
// middle split is possible. Not SGR-aware — the distinguishing-labels
// cell is plain text.
func ellipsizeMiddle(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= len(format.EllipsizeSuffix) {
		return format.Ellipsize(s, w)
	}
	keep := w - lipgloss.Width(format.EllipsizeSuffix)
	head := (keep + 1) / 2
	tail := keep - head
	headStr := format.Truncate(s, head)
	tailStr := truncateLeft(s, tail)
	return headStr + format.EllipsizeSuffix + tailStr
}

// truncateLeft returns the suffix of s whose rendered width is at most
// w cells, walking runes from the end so the discriminating tail is
// preserved. Mirrors format.Truncate from the other side.
func truncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	runes := []rune(s)
	used := 0
	cut := len(runes)
	for i, r := range slices.Backward(runes) {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		used += rw
		cut = i
	}
	return string(runes[cut:])
}

// columnWidths returns the SEVERITY, INSTANCE (flex), STATE, AGE
// widths via the duf-style distributor. INSTANCE is the unbounded
// weight-1 flex column; the rest are fixed at max(min, content).
func (p *Page) columnWidths(width int) []int {
	budget := max(0, width-format.RowPrefixCols)
	return format.Distribute(p.columnSpecs(), budget, colSeparator)
}

func (p *Page) columnSpecs() []format.Column {
	const (
		sevMin      = 12
		stateMin    = 8
		ageRelMin   = 12
		ageAbsMin   = 20
		instanceMin = 10
	)
	ageMin := ageRelMin
	if p.timeFormat == timerender.Absolute {
		ageMin = ageAbsMin
	}

	sevContent := lipgloss.Width("SEVERITY")
	stateContent := lipgloss.Width("STATE")
	ageContent := lipgloss.Width("AGE")
	for _, e := range p.view {
		if w := lipgloss.Width(severityOf(e.a)); w > sevContent {
			sevContent = w
		}
		if w := lipgloss.Width(stateToken(e.a.State, p.stateFormat)); w > stateContent {
			stateContent = w
		}
	}
	if ageMin > ageContent {
		ageContent = ageMin
	}

	return []format.Column{
		{Min: sevMin, Content: max(sevMin, sevContent), Weight: 0},
		{Min: instanceMin, Content: format.FlexUnbounded, Weight: 1},
		{Min: stateMin, Content: max(stateMin, stateContent), Weight: 0},
		{Min: ageMin, Content: ageContent, Weight: 0},
	}
}

// stateToken renders one instance's state per the active density.
// Full echoes the AM-native word; Compact emits the two-letter
// abbreviation (chosen to avoid colliding visually with the `s` / `S`
// silence verbs). Unknown states fall through to their full string in
// both modes so a non-conforming upstream value stays legible.
func stateToken(s backend.AlertState, f stateformat.Format) string {
	if f != stateformat.Compact {
		return string(s)
	}
	switch s {
	case backend.AlertStateActive:
		return "ac"
	case backend.AlertStateSuppressed:
		return "su"
	case backend.AlertStateUnprocessed:
		return "un"
	}
	return string(s)
}

// formatTime renders ts per the page's active time format.
func (p *Page) formatTime(ts time.Time) string {
	return timerender.Display(p.timeFormat, p.now(), ts)
}

// severityOf returns the printable severity label, "—" when absent.
func severityOf(a backend.Alert) string {
	if v, ok := a.Labels["severity"]; ok && v != "" {
		return v
	}
	return "—"
}

// severityStyle returns the lipgloss style for a's severity so the
// SEVERITY cell can be foreground-tinted. Unknown / missing values
// fall back to Severity.Unknown.
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

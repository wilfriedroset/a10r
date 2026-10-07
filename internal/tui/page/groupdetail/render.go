// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/table"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	l := p.scroll.Layout(p.columns(), width)
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
	headerLine := p.renderHeader(l)
	rows := p.renderRows(l, width, height-1-bandLines-stripLines)
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
	if p.FilterBuffer() != "" || p.stateFilter != "" {
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

// renderHeader returns the column-title row. The two foreground
// renderers are fg-only so the header keeps the terminal default
// background: a palette background inside the unstyled body frame
// paints a coloured stripe. The active column takes the second tint,
// which pairs with the arrow to give two cues for which sort is live.
func (p *Page) renderHeader(l table.Layout) string {
	return l.Header(
		table.Sort{Arrow: p.sorter.ArrowFor, Active: p.sorter.IsActive},
		table.Chrome{Fg: p.styles.Table.HeaderFg, ActiveFg: p.styles.Table.HeaderActiveFg},
	)
}

// sortKeyState labels the STATE column. It is not a sort key — the
// column is non-sortable on this page — but every column carries one.
const sortKeyState = "state"

func (p *Page) renderRows(l table.Layout, width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	end := min(p.TopRow()+maxRows, len(p.view))
	// An open range previews as marked rows; the keys only reach
	// p.marks on commit, so the span is resolved per frame.
	visual := listpage.VisualPreview(&p.Base, p.view, markKey)
	spans := p.FilterSpans()
	var b strings.Builder
	b.Grow((end - p.TopRow()) * width * 2)
	for i := p.TopRow(); i < end; i++ {
		b.WriteString(l.Row(p.row(i, visual.Covers(i), spans), width))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// row builds one instance row at view index i.
//
// Colour follows instance state: a FIRING (active) instance that is
// neither the cursor (its row-level highlight wins) nor marked gets
// the full treatment — its SEVERITY cell tints and its distinguishing
// labels take the YAML palette so a k=v pair reads consistently across
// the TUI. Suppressed and unprocessed instances recede: the whole row
// dims, so the firing ones the operator can still act on stand out.
// The cursor and marked rows keep their row-level wrap (nested ANSI
// inside it is fragile), so their labels keep no palette of their own.
func (p *Page) row(i int, previewed bool, spans func(string) [][2]int) table.Row {
	entry := p.view[i]
	a := entry.a
	ageLabel := p.formatTime(a.StartsAt)
	if ageLabel == "" {
		ageLabel = "—"
	}
	_, marked := p.marks[a.Fingerprint]
	marked = marked || previewed
	mark := " "
	if marked {
		mark = "✓"
	}
	isCursor := i == p.Index()
	isActive := a.State == backend.AlertStateActive
	rowStyled := isCursor || marked || !isActive
	hl := format.HighlighterFor(spans, p.styles.Table.MatchFg, rowStyled)
	// Bound once: a method value per cell escapes to the heap.
	paint := hl.Text

	sev := table.Cell{Text: severityOf(a), Paint: paint}
	summary := table.Cell{Text: entry.distinguishSummary, Paint: paint}
	if !rowStyled {
		tint := p.styles.Severity.ForLabel(a.Labels["severity"])
		sev.Paint = func(shown string) string { return hl.Cell(shown, tint) }
		summary.Paint = func(shown string) string { return p.styleDistinguish(shown, hl) }
	}
	cells := make([]table.Cell, 0, 4+len(p.labels.Shown()))
	cells = append(cells, sev, summary)
	for _, c := range p.labels.Shown() {
		cells = append(cells, table.Cell{Text: table.LabelCell(entry.labelCells, c.Index), Paint: paint})
	}
	cells = append(cells,
		table.Cell{Text: stateToken(a.State, p.stateFormat), Paint: paint},
		table.Cell{Text: ageLabel, Paint: paint},
	)
	prefix := "  "
	if isCursor {
		prefix = "▸ "
	}
	return table.Row{Prefix: prefix + mark + " ", Cells: cells, Style: p.rowStyle(a, isCursor, marked)}
}

// rowStyle returns the style wrapping the whole row, or the zero style
// for a plain one. Precedence: cursor > marked > dimmed. Cursor wraps
// in fg+bg (the "you are here" signal); marked and dimmed change the
// foreground only so the row keeps the body background.
func (p *Page) rowStyle(a backend.Alert, isCursor, marked bool) lipgloss.Style {
	switch {
	case isCursor:
		return p.styles.Table.CursorOver(p.styles.Severity.ForLabel(a.Labels["severity"]).GetForeground())
	case marked:
		return p.styles.Table.MarkedFg
	case a.State != backend.AlertStateActive:
		return p.styles.Table.DimmedFg
	}
	return lipgloss.Style{}
}

// styleDistinguish colours an already-clipped distinguishing-labels
// cell with the YAML palette (name / `=` / value / separator). It runs
// AFTER the middle-out clip on
// the plain string, so the ellipsis stays correct and colouring never
// changes the cell's width — layout is unaffected. A fragment the clip
// left without an `=` (a rare middle-cut artefact) renders in the value
// colour. hl paints each piece on its own, so a filter match that
// crosses a key, `=` and value boundary paints nothing.
func (p *Page) styleDistinguish(clipped string, hl format.Highlighter) string {
	if clipped == "" {
		return clipped
	}
	pairs := strings.Split(clipped, " · ")
	for i, pair := range pairs {
		name, val, ok := strings.Cut(pair, "=")
		if !ok {
			pairs[i] = hl.Cell(pair, p.styles.YAML.Value)
			continue
		}
		pairs[i] = hl.Cell(name, p.styles.YAML.Key) +
			p.styles.YAML.Punct.Render("=") +
			hl.Cell(val, p.styles.YAML.Value)
	}
	return strings.Join(pairs, p.styles.YAML.Punct.Render(" · "))
}

func (p *Page) columns() []table.Column { return p.columnsWith(p.labels.Columns()) }

// sortAxes takes its order from every declared label column, not the
// shown tier, so Shift+W never reorders the walk; SetHidden steps over
// the wide ones instead.
func (p *Page) sortAxes() []tablesort.Column[instanceEntry] {
	all := p.labels.All()
	return table.SortAxes(p.columnsWith(table.LabelColumns(all, nil)), instanceSortColumns(all))
}

// columnsWith is the rendered column order, declared once. It is the only
// place on this page that knows where the user-declared block splices
// into the built-ins. Content widths come from the filtered view, so
// the layout reacts to the data the operator is looking at. Header
// labels need no measuring — the layout pass floors every column at
// its own header.
func (p *Page) columnsWith(labels []table.Column) []table.Column {
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
	sevContent, stateContent := 0, 0
	for _, e := range p.view {
		sevContent = max(sevContent, lipgloss.Width(severityOf(e.a)))
		stateContent = max(stateContent, lipgloss.Width(stateToken(e.a.State, p.stateFormat)))
	}

	out := make([]table.Column, 0, 4+len(labels))
	out = append(out,
		table.Column{Key: sortKeySeverity, Title: "SEVERITY", Sortable: true, Min: sevMin, Content: max(sevMin, sevContent)},
		// INSTANCE is the unbounded flex column: FlexUnbounded stops the
		// allocator capping it, so it takes every leftover cell rather
		// than leaving dead space beside the labels being scanned.
		table.Column{
			Key: sortKeyInstance, Title: "INSTANCE", Sortable: true,
			Min: instanceMin, Content: format.FlexUnbounded, Weight: 1, Clip: table.ClipMiddle,
		},
	)
	out = append(out, labels...)
	return append(out,
		table.Column{Key: sortKeyState, Title: "STATE", Min: stateMin, Content: max(stateMin, stateContent)},
		table.Column{Key: sortKeyAge, Title: "AGE", Sortable: true, Min: ageMin, Content: ageMin},
	)
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

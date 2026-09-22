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
	"github.com/wilfriedroset/a10r/internal/tui/page/labelcol"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	// The scroll keys need to know whether the row fits, and no width
	// reaches the page at key time.
	p.scroll.Width = width
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

// renderHeader returns the column-title row with a sort marker on the
// active column. SEVERITY, INSTANCE (flex), STATE, AGE — no TENANT,
// no COUNT.
func (p *Page) renderHeader(width int) string {
	keys := p.headerKeys()
	widths, win := p.columnWidths(width)
	headerFg := p.styles.Table.HeaderFg
	activeFg := p.styles.Table.HeaderActiveFg

	var b strings.Builder
	b.WriteString(format.ScrollPrefix(win.ClipLeft))
	for j, ci := range win.Cols {
		if j > 0 {
			b.WriteString(colSep)
		}
		k := keys[ci]
		label := p.headerTitle(k)
		// STATE has no sort column; only the sortable columns get an
		// arrow / active tint.
		if arrow := p.sorter.ArrowFor(k); arrow != "" {
			label = label + " " + arrow
		}
		padded := format.PadRight(label, widths[j])
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

// sortKeyState labels the STATE column header. It is NOT a sort key —
// the column is non-sortable on this page — but the header renderer
// walks a uniform key list, so the label lives here alongside the
// real keys.
const sortKeyState = "state"

// headerKeys is the header's copy of the rendered column order, with
// the user-declared block spliced between INSTANCE and STATE.
// renderRow, padColumns and columnSpecs splice at the same point;
// this function is not the shared source of that order.
func (p *Page) headerKeys() []string {
	out := make([]string, 0, 4+len(p.shownCols))
	out = append(out, sortKeySeverity, sortKeyInstance)
	for _, c := range p.shownCols {
		out = append(out, c.Key)
	}
	return append(out, sortKeyState, sortKeyAge)
}

// headerTitle maps a header key to its rendered title. A user column
// carries its own, already upper-cased by the config loader.
func (p *Page) headerTitle(k string) string {
	for _, c := range p.labelCols {
		if c.Key == k {
			return c.Title
		}
	}
	if k == sortKeyState {
		return "STATE"
	}
	return strings.ToUpper(k)
}

// labelBlockStart is the row index the user-declared column block
// begins at: after SEVERITY and INSTANCE. No TENANT column on this
// page, so it never shifts.
const labelBlockStart = 2

func (p *Page) isLabelColumn(i int) bool {
	return i >= labelBlockStart && i < labelBlockStart+len(p.shownCols)
}

// measureLabelColumns measures each user-declared column over the
// whole filtered view, so a vertical scroll never shifts a width. A
// column with a configured Width needs no measuring and is left at
// zero, because labelColumnSpecs pins it before it reads this slice.
// recompute calls this once per row change rather than the renderer
// calling it once per frame.
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
		for j := range p.view {
			if w := lipgloss.Width(labelCellAt(&p.view[j], c.Index)); w > content {
				content = w
			}
		}
		out[i] = content
	}
	return out
}

// labelColumnSpecs turns the measured widths into allocator columns.
// A measured column flexes rather than reserving its full width:
// label values run long, and a weight-0 request that wide pushes the
// allocator into its proportional shrink, which takes the built-in
// columns below their own floors. Flexing reserves only the floor and
// grows into what is left alongside INSTANCE. A column with a
// configured width is pinned there, because that is what the operator
// asked for.
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

// labelColumnWidthFloor is the smallest a measured user column
// shrinks to before the allocator starts taking cells from the
// built-ins.
const labelColumnWidthFloor = 6

// renderRows returns the visible window of data rows, reconciling the
// scroll window against the cursor each frame.
//
// Colour follows instance state: a FIRING (active) instance that is
// neither the cursor (its row-level highlight wins) nor marked gets the
// full treatment — its SEVERITY cell tints and its distinguishing
// labels take the YAML palette so a k=v pair reads consistently
// across the TUI. Suppressed and unprocessed
// instances recede: the whole row dims, so the firing ones the operator
// can still act on stand out. The cursor and marked rows keep their
// row-level wrap (nested ANSI inside it is fragile), so their labels
// stay plain under the wrap.
func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	end := min(p.TopRow()+maxRows, len(p.view))
	cols, win := p.columnWidths(width)
	// A scrolled row renumbers its columns, so the flex width comes
	// from the window, not from the spec position.
	flexW := 0
	for j, ci := range win.Cols {
		if ci == flexColumnIndex {
			flexW = cols[j]
			break
		}
	}
	// An open range previews as marked rows; the keys only reach
	// p.marks on commit, so the span is resolved per frame.
	visual := listpage.VisualPreview(&p.Base, p.view, markKey)
	var b strings.Builder
	b.Grow((end - p.TopRow()) * width * 2)
	for i := p.TopRow(); i < end; i++ {
		b.WriteString(p.renderRow(i, cols, win, flexW, width, visual.Covers(i)))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderRow renders one instance row at the pre-computed column
// widths. See renderRows for the colour-by-state contract.
func (p *Page) renderRow(i int, cols []int, win format.Window, flexW, width int, previewed bool) string {
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
	colour := isActive && !isCursor && !marked

	sevCell := severityOf(a)
	// Clip the distinguishing labels on the PLAIN string (so the
	// middle-out ellipsis and column widths stay correct), then colour
	// the result — colouring never changes the cell's width.
	labels := ellipsizeMiddle(entry.distinguishSummary, flexW)
	if colour {
		sevCell = p.styles.Severity.ForLabel(a.Labels["severity"]).Render(sevCell)
		labels = p.styleDistinguish(labels)
	}
	prefix := "  "
	if isCursor {
		prefix = "▸ "
	}
	row := make([]string, 0, 4+len(p.shownCols))
	row = append(row, sevCell, labels)
	for _, c := range p.shownCols {
		row = append(row, labelCellAt(&entry, c.Index))
	}
	row = append(row, stateToken(a.State, p.stateFormat), ageLabel)
	line := format.PadRight(prefix+mark+" "+p.padColumns(row, cols, win), width)
	switch {
	case isCursor:
		return p.styles.Table.CursorOver(p.styles.Severity.ForLabel(a.Labels["severity"]).GetForeground()).Render(line)
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
// never fuse. Built-in cells arrive pre-clipped — renderRows
// middle-clips the flex distinguishing-labels cell (and optionally
// colours it) before calling. A user-declared cell is clipped here
// instead, because its width is only known once the allocator has
// run.
func (p *Page) padColumns(parts []string, cols []int, win format.Window) string {
	var b strings.Builder
	for j, ci := range win.Cols {
		if ci >= len(parts) {
			break
		}
		if j > 0 {
			b.WriteString(colSep)
		}
		v := parts[ci]
		if p.isLabelColumn(ci) {
			v = format.Ellipsize(v, cols[j])
		}
		b.WriteString(format.PadRight(v, cols[j]))
	}
	return b.String()
}

// styleDistinguish colours an already-clipped distinguishing-labels
// cell with the YAML palette (name / `=` / value / separator). It runs
// AFTER the middle-out clip on
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
func (p *Page) columnWidths(width int) ([]int, format.Window) {
	specs := p.columnSpecs()
	budget := max(0, width-format.RowPrefixCols)
	win := format.WindowAt(specs, budget, colSeparator, p.scroll.Offset)
	if len(win.Cols) < win.Total {
		// A clipped row keeps one cell out of the column budget for the
		// ">" marker, so the header never runs past the body width and
		// wraps. Re-running the window on the smaller budget can only
		// drop a further column, never bring one back.
		budget = max(0, budget-1)
		win = format.WindowAt(specs, budget, colSeparator, p.scroll.Offset)
	}
	shown := make([]format.Column, len(win.Cols))
	for i, ci := range win.Cols {
		shown[i] = specs[ci]
	}
	return format.Distribute(shown, budget, colSeparator), win
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

	out := make([]format.Column, 0, 4+len(p.shownCols))
	out = append(out,
		format.Column{Min: sevMin, Content: max(sevMin, sevContent), Weight: 0},
		format.Column{Min: instanceMin, Content: format.FlexUnbounded, Weight: 1},
	)
	out = append(out, p.labelColumnSpecs()...)
	return append(out,
		format.Column{Min: stateMin, Content: max(stateMin, stateContent), Weight: 0},
		format.Column{Min: ageMin, Content: ageContent, Weight: 0},
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

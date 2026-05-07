// SPDX-License-Identifier: Apache-2.0

// Package panel renders the k9s-style top panel and bordered
// body. The top panel is a 3-column row: labelled info on the
// left, action shortcuts in the middle, ASCII logo on the right.
// The body wrapper draws a single-line border with the page
// title centred in the top edge.
package panel

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// titleStructRE captures the standard "subject(scope)[count]"
// shape that pages return from their Title() method (see
// panel.Title). Group 1 = subject, group 2 = scope (inside `()`),
// group 4 = count digits inside `[]`, group 5 = optional trailing
// segment (a filter glyph, "loading…" suffix, etc.). Titles that
// don't match (cold-load spinners, modals) get the plain Frame.
// Title styling instead.
var titleStructRE = regexp.MustCompile(`^(.*?)\(([^()]*)\)(\[(\d+)\])?(.*)$`)

// styleTitle applies the per-segment k9s title colouring to the
// raw title returned by a page. Layout mirrors k9s NSTitleFmt:
//
//	subject(scope)[count]
//	└─────┘ └───┘  └───┘
//	  fg+bold  hilite+bold  counter+bold
//	         └─┘   └─┘
//	          fg     fg
//
// Anything outside that shape (loading spinner, modal label) gets
// a single-tone Frame.Title render — the title stays readable
// instead of falling back to terminal default.
func styleTitle(raw string, styles theme.Styles) string {
	m := titleStructRE.FindStringSubmatch(raw)
	if m == nil {
		return styles.Frame.Title.Bold(true).Render(raw)
	}
	subject, scope, count, trailing := m[1], m[2], m[4], m[5]
	titleBold := styles.Frame.Title.Bold(true)
	hiliteBold := styles.Frame.TitleHighlight.Bold(true)
	titlePlain := styles.Frame.Title
	var b strings.Builder
	b.WriteString(titleBold.Render(subject + "("))
	b.WriteString(hiliteBold.Render(scope))
	b.WriteString(titlePlain.Render(")"))
	if count != "" {
		b.WriteString(titlePlain.Render("["))
		b.WriteString(styles.Frame.TitleCounter.Bold(true).Render(count))
		b.WriteString(titlePlain.Render("]"))
	}
	if trailing != "" {
		b.WriteString(titlePlain.Render(trailing))
	}
	return b.String()
}

// InfoLine is one labelled row in the panel's info column. K9s
// uses these for context / cluster / user / version / cpu / mem;
// a10r uses tenant / backend / version / count.
type InfoLine struct {
	Label string
	Value string
}

// TenantBinding is one entry in the panel's tenant-shortcut
// column. K9s shows numeric shortcuts for the configured
// namespaces (`<0> all`, `<1> ns-1`, …); a10r mirrors the
// shape with one row per configured backend plus the synthetic
// `<0> all` selector.
type TenantBinding struct {
	Key  string
	Name string
}

// State bundles every input the renderer needs. Stateless: the
// app shell rebuilds it every frame from poll snapshots and the
// active page's metadata.
type State struct {
	Width int
	// Info is the labelled column on the left.
	Info []InfoLine
	// Tenants are the numeric tenant shortcuts surfaced in their
	// own column. Empty hides the column.
	Tenants []TenantBinding
	// Hints are the page's bindings rendered as a `<key> Action`
	// table in the middle. Auto-built from the active page's
	// Bindings() so adding a binding there shows up here.
	Hints []action.Action
	// Logo is the ASCII art on the right. Empty hides the column.
	Logo string
}

// gridCols caps how many parallel columns the tenants and hints
// shortcut grids can grow to. Three keeps the panel readable on
// narrow terminals — k9s does the same for its namespace and menu
// strips.
const gridCols = 3

// unboundedRows is the rowsBudget callers pass when there's no
// logo (and therefore no natural ceiling on the panel height).
// Larger than any realistic tenant or hint count; a typed sentinel
// reads cleaner than a bare `1 << 30`.
const unboundedRows = 1 << 30

// RenderTop produces the multi-line top panel string. Built
// row-by-row so multi-line columns (the logo in particular)
// align consistently across every row. Layout:
//
//	┌─────────┬──────────────┬───────────────┬──────────┐
//	│ info    │ tenants      │ hints         │ logo     │
//	└─────────┴──────────────┴───────────────┴──────────┘
//
// The tenants column appears only when state.Tenants is non-
// empty; the logo drops first when the width budget is tight.
// Tenants and hints are laid out as up-to-3-column k9s-style
// grids so a long backend list or hint set doesn't push the panel
// past the logo's height. Items past `gridCols × logoHeight`
// silently clip — the panel never grows taller than the logo.
// The output is exactly state.Width columns wide.
func RenderTop(state State, styles theme.Styles) string {
	if state.Width <= 0 {
		return ""
	}
	logoLines := splitNonEmpty(state.Logo)
	rowsBudget := len(logoLines)
	if rowsBudget <= 0 {
		// Empty logo — fall back to a generous budget so callers that
		// strip the logo still get every entry rendered. The narrow-
		// width drop below clears the logo *after* the grid has been
		// built, so this branch only fires when the caller explicitly
		// passes Logo == "".
		rowsBudget = unboundedRows
	}
	infoLines := clipLines(renderInfoLines(state.Info, styles), rowsBudget)
	tenantLines := renderTenantLines(state.Tenants, rowsBudget, styles)
	hintLines := renderHintLines(state.Hints, rowsBudget, styles)

	infoW := maxWidth(infoLines)
	tenantW := maxWidth(tenantLines)
	hintW := maxWidth(hintLines)
	logoW := maxWidth(logoLines)

	const gap = 2
	gaps := 0
	for _, w := range []int{infoW, tenantW, hintW, logoW} {
		if w > 0 {
			gaps++
		}
	}
	gaps = max(gaps-1, 0) * gap

	if infoW+tenantW+hintW+logoW+gaps > state.Width {
		// Drop the logo first when the budget is tight.
		logoLines = nil
		logoW = 0
	}

	rows := max(
		len(infoLines),
		len(tenantLines),
		len(hintLines),
		len(logoLines),
	)
	if rows == 0 {
		return ""
	}

	out := make([]string, rows)
	for i := range rows {
		info := padRight(getLine(infoLines, i), infoW)
		tenants := padRight(getLine(tenantLines, i), tenantW)
		hint := padRight(getLine(hintLines, i), hintW)
		// Pad every logo line to the SAME logoW so the right-fill
		// is uniform across rows and the logo block doesn't stagger.
		// Tint with body.logoColor for k9s parity — the logo lights
		// up in the skin's accent (mauve in mocha) instead of
		// rendering as plain body fg.
		logoLine := getLine(logoLines, i)
		logoPadded := padRight(logoLine, logoW)
		logo := logoPadded
		if logoLine != "" {
			logo = styles.Body.Logo.Render(logoPadded)
		}

		// Build left-to-right, inserting a 2-space gap only when
		// the next column is non-empty.
		var sb strings.Builder
		first := true
		appendCol := func(s string, w int) {
			if w == 0 {
				return
			}
			if !first {
				sb.WriteString(strings.Repeat(" ", gap))
			}
			sb.WriteString(s)
			first = false
		}
		appendCol(info, infoW)
		appendCol(tenants, tenantW)
		appendCol(hint, hintW)
		// Logo: right-aligned with whatever fill is left.
		left := sb.String()
		var line string
		if logoW > 0 {
			rightFill := max(state.Width-lipgloss.Width(left)-logoW, gap)
			line = left + strings.Repeat(" ", rightFill) + logo
		} else {
			line = left
		}
		out[i] = padRight(line, state.Width)
	}
	return strings.Join(out, "\n")
}

// renderTenantLines formats the tenant-shortcut column as a
// k9s-style column-major grid of `<key> name` cells. Width is
// capped at gridCols columns and rowsBudget rows; items past the
// cap silently drop so the panel never grows taller than the
// logo. Each cell is styled with the hint key colour and bolded
// to distinguish tenant / namespace shortcuts from regular action
// shortcuts.
func renderTenantLines(tenants []TenantBinding, rowsBudget int, styles theme.Styles) []string {
	if len(tenants) == 0 {
		return nil
	}
	maxKey := 0
	for _, t := range tenants {
		w := lipgloss.Width("<" + t.Key + ">")
		if w > maxKey {
			maxKey = w
		}
	}
	keyStyle := styles.Hint.HelpKey.Bold(true)
	nameStyle := theme.FgOnly(styles.Hint.Default.GetForeground()).Bold(true)
	cells := make([]string, len(tenants))
	for i, t := range tenants {
		key := keyStyle.Render("<" + t.Key + ">")
		pad := strings.Repeat(" ", maxKey-lipgloss.Width(key)+1)
		cells[i] = key + pad + nameStyle.Render(t.Name)
	}
	return gridLines(cells, rowsBudget)
}

// gridLines lays cells out in a column-major grid of up to
// gridCols columns and rowsBudget rows, padding each cell to the
// widest cell so the columns line up. Items past the cap are
// silently clipped — the caller can detect this by comparing
// len(cells) before the call against the rendered output.
//
// Layout rule: when len(cells) ≤ rowsBudget the grid stays
// single-column (one item per row, top-down). Otherwise cols is
// the smallest count that fits (capped at gridCols) and the rows
// budget fills entirely; remaining items past the capacity drop.
func gridLines(cells []string, rowsBudget int) []string {
	if len(cells) == 0 || rowsBudget <= 0 {
		return nil
	}
	cols, rows := 1, len(cells)
	if len(cells) > rowsBudget {
		cols = min((len(cells)+rowsBudget-1)/rowsBudget, gridCols)
		rows = rowsBudget
	}
	if capacity := cols * rows; len(cells) > capacity {
		cells = cells[:capacity]
	}
	cellW := 0
	for _, c := range cells {
		if w := lipgloss.Width(c); w > cellW {
			cellW = w
		}
	}
	const colGap = 2
	out := make([]string, rows)
	for r := range rows {
		var sb strings.Builder
		for col := range cols {
			idx := col*rows + r
			if idx >= len(cells) {
				break
			}
			if col > 0 {
				sb.WriteString(strings.Repeat(" ", colGap))
			}
			cell := cells[idx]
			sb.WriteString(cell)
			sb.WriteString(strings.Repeat(" ", cellW-lipgloss.Width(cell)))
		}
		out[r] = sb.String()
	}
	return out
}

// clipLines returns lines truncated to at most maxRows entries.
// Used to keep the labelled info column from pushing the panel
// past the logo's height. nil-safe.
func clipLines(lines []string, maxRows int) []string {
	if len(lines) <= maxRows {
		return lines
	}
	return lines[:maxRows]
}

// renderInfoLines splits the info column into per-row lines, with
// labels right-aligned to a common width so the values line up.
func renderInfoLines(lines []InfoLine, styles theme.Styles) []string {
	if len(lines) == 0 {
		return nil
	}
	maxLabel := 0
	for _, l := range lines {
		if w := lipgloss.Width(l.Label); w > maxLabel {
			maxLabel = w
		}
	}
	out := make([]string, len(lines))
	labelStyle := theme.FgOnly(styles.Hint.Default.GetForeground())
	// Values use the body's foreground only — same fg-only
	// treatment the labels get above. Calling Body.Default would
	// paint the body's bg behind the value, which renders as a
	// stripe of mismatched colour against the surrounding empty
	// cells in the panel zone.
	valueStyle := theme.FgOnly(styles.Body.Default.GetForeground())
	for i, l := range lines {
		pad := strings.Repeat(" ", maxLabel-lipgloss.Width(l.Label))
		out[i] = labelStyle.Render(l.Label+":") + pad + " " + valueStyle.Render(l.Value)
	}
	return out
}

// renderHintLines formats the hint column as a k9s-style column-
// major grid of `<key> Description` cells, capped the same way
// the tenant shortcuts are. Mirrors k9s's frame.menu zone.
func renderHintLines(hints []action.Action, rowsBudget int, styles theme.Styles) []string {
	if len(hints) == 0 {
		return nil
	}
	maxKey := 0
	for _, a := range hints {
		w := lipgloss.Width("<" + a.Key + ">")
		if w > maxKey {
			maxKey = w
		}
	}
	descStyle := theme.FgOnly(styles.Hint.Default.GetForeground())
	cells := make([]string, len(hints))
	for i, a := range hints {
		keyStyle := styles.Hint.Key.Bold(true)
		if a.Key == "?" {
			keyStyle = styles.Hint.HelpKey.Bold(true)
		}
		key := keyStyle.Render("<" + a.Key + ">")
		pad := strings.Repeat(" ", maxKey-lipgloss.Width(key)+1)
		cells[i] = key + pad + descStyle.Render(a.Description)
	}
	return gridLines(cells, rowsBudget)
}

// splitNonEmpty splits s on \n, returning nil for empty input.
func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// getLine returns lines[i] when i is in range, otherwise "" so
// the row builder can compose mismatched-height columns.
func getLine(lines []string, i int) string {
	if i >= len(lines) {
		return ""
	}
	return lines[i]
}

// maxWidth returns the widest visual width across lines.
func maxWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if x := lipgloss.Width(l); x > w {
			w = x
		}
	}
	return w
}

// padRight pads s with trailing spaces to width w. Truncates
// when s is wider (rare in panel rendering).
func padRight(s string, w int) string {
	cur := lipgloss.Width(s)
	if cur >= w {
		return s
	}
	return s + strings.Repeat(" ", w-cur)
}

// RenderBody wraps the page's body content in a single-line
// border with title centred in the top edge and an optional
// footer label centred in the bottom edge — the k9s look:
//
//	┌────────── alerts(prod)[531] ──────────┐
//	│ <body>                                │
//	└─────────── next refresh 26s ──────────┘
//
// Returns a string exactly width columns wide and at most height
// rows tall. The body content is sliced / padded to fit the
// inner rectangle (width-2, height-2). Empty footer renders the
// bottom edge as a plain rule.
//
// Border characters are foreground-tinted with the skin's
// `frame.border.fgColor` (k9s parity); the title text is tinted
// with `frame.title.fgColor`. The inner content is left untouched
// so per-cell colouring inside the body keeps showing through.
func RenderBody(width, height int, body, title, footer string, styles theme.Styles) string {
	if width < 4 || height < 2 {
		return body
	}
	innerWidth := width - 2
	innerHeight := height - 2

	// Top border with embedded title: "┌── title ──┐".
	top := buildTitleBorder(innerWidth, title, styles)

	// Inner body: split body into lines, pad / slice to fit inner.
	lines := strings.Split(body, "\n")
	if len(lines) > innerHeight {
		lines = lines[:innerHeight]
	}
	for len(lines) < innerHeight {
		lines = append(lines, "")
	}
	bar := styles.Frame.Border.Render("│")
	for i, l := range lines {
		w := lipgloss.Width(l)
		if w > innerWidth {
			l = truncate(l, innerWidth)
		} else if w < innerWidth {
			l += strings.Repeat(" ", innerWidth-w)
		}
		lines[i] = bar + l + bar
	}

	// Bottom border, optionally embedding a label the same way
	// the title sits in the top edge.
	bottom := buildFooterBorder(innerWidth, footer, styles)

	return top + "\n" + strings.Join(lines, "\n") + "\n" + bottom
}

// RenderFrame wraps a single-line `body` in a 3-line bordered box
// matching the body panel's frame:
//
//	┌──────────────┐
//	│ body         │
//	└──────────────┘
//
// Used by the App for the `:` / `/` prompt panel. The body is
// truncated when it exceeds the inner width; a hard upper bound
// since the prompt is keyboard-driven and the user can only enter
// what they see. Border is `frame.border.fgColor`-tinted so the
// prompt panel matches the body panel's frame.
func RenderFrame(width int, body string, styles theme.Styles) string {
	if width < 4 {
		return body
	}
	innerWidth := width - 2
	w := lipgloss.Width(body)
	switch {
	case w > innerWidth:
		body = truncate(body, innerWidth)
	case w < innerWidth:
		body += strings.Repeat(" ", innerWidth-w)
	}
	border := styles.Frame.Border
	top := border.Render("┌" + strings.Repeat("─", innerWidth) + "┐")
	bottom := border.Render("└" + strings.Repeat("─", innerWidth) + "┘")
	bar := border.Render("│")
	return top + "\n" + bar + body + bar + "\n" + bottom
}

// buildTitleBorder draws "┌── title ──┐" with the title centred
// and tinted in `frame.title.fgColor`. The horizontal rules get
// `frame.border.fgColor`. Falls back to a plain (still tinted)
// border when the title is too long for the inner width (rare on
// terminals ≥ 80 cols).
func buildTitleBorder(innerWidth int, title string, styles theme.Styles) string {
	return buildLabelBorder(innerWidth, title, "┌", "┐", styles)
}

// buildFooterBorder is the bottom-edge counterpart: same layout,
// `└` / `┘` corners. Empty label renders a plain rule so pages
// without ambient state to surface still get a clean frame.
func buildFooterBorder(innerWidth int, footer string, styles theme.Styles) string {
	return buildLabelBorder(innerWidth, footer, "└", "┘", styles)
}

// buildLabelBorder draws "<L>── label ──<R>" with the label
// centred. Border characters are border-tinted via styles.Frame.
// Border. The label text is segment-styled by styleTitle to match
// k9s's three-tone title (subject / scope-inside-parens / count-
// inside-brackets) — note we measure the visual width of the
// label glyphs without the SGR codes so centring stays correct.
// Falls back to a plain (border-tinted) rule when label is empty
// or would not leave at least 2 chars of border on each side.
func buildLabelBorder(innerWidth int, label, leftCorner, rightCorner string, styles theme.Styles) string {
	border := styles.Frame.Border
	if label == "" {
		return border.Render(leftCorner + strings.Repeat("─", innerWidth) + rightCorner)
	}
	if lipgloss.Width(label)+4 > innerWidth {
		label = truncate(label, innerWidth-4)
	}
	styled := styleTitle(label, styles)
	wrapped := " " + styled + " "
	labelW := lipgloss.Width(label) + 2 // styled width == raw width + 2 spaces
	left := (innerWidth - labelW) / 2
	right := innerWidth - labelW - left
	leftRule := border.Render(leftCorner + strings.Repeat("─", left))
	rightRule := border.Render(strings.Repeat("─", right) + rightCorner)
	return leftRule + wrapped + rightRule
}

// truncate cuts s to at most w columns. Lipgloss-aware so a
// future emoji title doesn't clip on a fractional rune.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

// Title formats the standard "<crumb>(<scope>)[<count>]" body
// title pages use. scope or count of "" / 0 is omitted.
func Title(crumb, scope string, count int) string {
	out := crumb
	if scope != "" {
		out += "(" + scope + ")"
	}
	if count > 0 {
		out += fmt.Sprintf("[%d]", count)
	}
	return out
}

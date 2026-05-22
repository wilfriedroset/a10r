// SPDX-License-Identifier: Apache-2.0

// Package panel renders the k9s-style top panel and bordered
// body. The top panel is a 3-column row: tenant shortcuts on the
// left, page action shortcuts in the middle, ASCII logo on the
// right. The body wrapper draws a single-line border with the
// page title centred in the top edge.
package panel

import (
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
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
func styleTitle(raw string, styles *theme.Styles) string {
	m := titleStructRE.FindStringSubmatch(raw)
	if m == nil {
		return styles.Frame.TitleBold.Render(raw)
	}
	subject, scope, count, trailing := m[1], m[2], m[4], m[5]
	titleBold := styles.Frame.TitleBold
	hiliteBold := styles.Frame.TitleHighlightBold
	titlePlain := styles.Frame.Title
	var b strings.Builder
	b.WriteString(titleBold.Render(subject + "("))
	b.WriteString(hiliteBold.Render(scope))
	b.WriteString(titlePlain.Render(")"))
	if count != "" {
		b.WriteString(titlePlain.Render("["))
		b.WriteString(styles.Frame.TitleCounterBold.Render(count))
		b.WriteString(titlePlain.Render("]"))
	}
	if trailing != "" {
		b.WriteString(titlePlain.Render(trailing))
	}
	return b.String()
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
	// Tenants are the numeric tenant shortcuts surfaced in the
	// leftmost column. Empty hides the column.
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
//	┌──────────────┬───────────────┬──────────┐
//	│ tenants      │ hints         │ logo     │
//	└──────────────┴───────────────┴──────────┘
//
// The tenants column appears only when state.Tenants is non-
// empty; the logo drops first when the width budget is tight.
// Tenants and hints are laid out as up-to-3-column k9s-style
// grids so a long backend list or hint set doesn't push the panel
// past the logo's height. Items past `gridCols × logoHeight`
// silently clip — the panel never grows taller than the logo.
// The output is exactly state.Width columns wide.
func RenderTop(state State, styles *theme.Styles) string {
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
	tenantLines := renderTenantLines(state.Tenants, rowsBudget, styles)
	tenantW := maxWidth(tenantLines)
	logoW := maxWidth(logoLines)

	// Hint grid renders against the budget left after tenants and (if
	// retained) the logo. Logo-drop happens first: if the natural-cols
	// hint render plus tenants + logo overflows state.Width, the logo
	// goes. The final hint pass then reflows cols 3→2→1 (and drops
	// trailing chips as a last resort) to fit availWidth.
	hintLines := renderHintLines(state.Hints, rowsBudget, unboundedRows, styles)
	hintW := maxWidth(hintLines)
	gaps := 0
	for _, w := range []int{tenantW, hintW, logoW} {
		if w > 0 {
			gaps++
		}
	}
	gaps = max(gaps-1, 0) * colGap

	if tenantW+hintW+logoW+gaps > state.Width {
		logoLines = nil
		logoW = 0
		availWidth := state.Width
		if tenantW > 0 {
			availWidth -= tenantW + colGap
		}
		hintLines = renderHintLines(state.Hints, rowsBudget, availWidth, styles)
		hintW = maxWidth(hintLines)
	}

	rows := max(
		len(tenantLines),
		len(hintLines),
		len(logoLines),
	)
	if rows == 0 {
		return ""
	}

	out := make([]string, rows)
	for i := range rows {
		out[i] = composeRow(rowParts{
			tenants: getLine(tenantLines, i),
			hint:    getLine(hintLines, i),
			logo:    getLine(logoLines, i),
			tenantW: tenantW,
			hintW:   hintW,
			logoW:   logoW,
			totalW:  state.Width,
		}, styles)
	}
	return strings.Join(out, "\n")
}

// rowParts bundles the per-row inputs composeRow walks. Grouped
// in a struct so the signature stays small as the panel grows.
type rowParts struct {
	tenants, hint, logo           string
	tenantW, hintW, logoW, totalW int
}

// composeRow assembles one panel row: tenants + gap + hint with
// the logo right-aligned to totalW. The two left columns each
// get padded to their natural width so multi-row content stays
// aligned; the logo lines get tinted with body.logoColor for k9s
// parity. SGRTruncate-then-PadRight is the belt-and-braces hard
// floor — the width-aware reflow should already keep rows within
// totalW, but a future regression would otherwise smear an active
// SGR style into the body chrome.
func composeRow(p rowParts, styles *theme.Styles) string {
	tenants := format.PadRight(p.tenants, p.tenantW)
	hint := format.PadRight(p.hint, p.hintW)
	logoPadded := format.PadRight(p.logo, p.logoW)
	logo := logoPadded
	if p.logo != "" {
		logo = styles.Body.Logo.Render(logoPadded)
	}
	left := joinCols(colGap, colPart{tenants, p.tenantW}, colPart{hint, p.hintW})
	line := left
	if p.logoW > 0 {
		rightFill := max(p.totalW-lipgloss.Width(left)-p.logoW, colGap)
		line = left + strings.Repeat(" ", rightFill) + logo
	}
	return format.PadRight(format.SGRTruncate(line, p.totalW), p.totalW)
}

// colPart is the (rendered, width) pair joinCols walks. width=0
// hides the column (no content, no leading gap).
type colPart struct {
	s string
	w int
}

// joinCols concatenates parts left-to-right with a `gap`-wide
// inter-column spacer, skipping any zero-width column. Pulled
// out of composeRow so the per-row builder stays linear.
func joinCols(gap int, parts ...colPart) string {
	var sb strings.Builder
	first := true
	for _, p := range parts {
		if p.w == 0 {
			continue
		}
		if !first {
			sb.WriteString(strings.Repeat(" ", gap))
		}
		sb.WriteString(p.s)
		first = false
	}
	return sb.String()
}

// renderTenantLines formats the tenant-shortcut column as a
// k9s-style column-major grid of `<key> name` cells. Width is
// capped at gridCols columns and rowsBudget rows; items past the
// cap silently drop so the panel never grows taller than the
// logo. Each cell is styled with the hint key colour and bolded
// to distinguish tenant / namespace shortcuts from regular action
// shortcuts.
func renderTenantLines(tenants []TenantBinding, rowsBudget int, styles *theme.Styles) []string {
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
	keyStyle := styles.Hint.HelpKeyBold
	nameStyle := styles.Hint.DefaultFgBold
	cells := make([]string, len(tenants))
	for i, t := range tenants {
		key := keyStyle.Render("<" + t.Key + ">")
		pad := strings.Repeat(" ", maxKey-lipgloss.Width(key)+1)
		cells[i] = key + pad + nameStyle.Render(t.Name)
	}
	return gridLines(cells, rowsBudget)
}

// colGap is the inter-cell spacing inside a grid AND the inter-
// zone spacing between the three top-panel zones — one source of
// truth so a future tweak (e.g. dropping to single-space chrome)
// stays uniform.
const colGap = 2

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
//
// Used for the tenant grid (cell-count-driven cols). The hint
// grid uses gridLinesWithWidth instead so it can reflow under
// width pressure.
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
	return renderGrid(cells, cols, rows, widestCell(cells))
}

// gridLinesWithWidth picks the largest cols ∈ {1, 2, 3} where
// `cols*cellW + (cols-1)*colGap ≤ availWidth`, with cellW the
// widest cell. If even 1 col at cellW won't fit, the trailing
// cell drops and cellW is recomputed against the remainder —
// repeated until the residual fits or no cells remain. Capacity
// past `cols*rowsBudget` clips from the tail the same way
// gridLines does.
//
// Drop-from-end matches the registration-order contract from
// ADR 0036: pages list bindings most-important-first, so the
// trailing chip is by construction the most expendable.
func gridLinesWithWidth(cells []string, rowsBudget, availWidth int) []string {
	if len(cells) == 0 || rowsBudget <= 0 {
		return nil
	}
	cellW := widestCell(cells)
	for len(cells) > 0 && cellW > availWidth {
		cells = cells[:len(cells)-1]
		cellW = widestCell(cells)
	}
	if len(cells) == 0 {
		return nil
	}
	cols := 1
	for c := gridCols; c > 1; c-- {
		if c*cellW+(c-1)*colGap <= availWidth {
			cols = c
			break
		}
	}
	rows := rowsBudget
	if len(cells) <= rowsBudget {
		// Few enough cells to stack in one column under rowsBudget;
		// keep them column-major in a single column regardless of the
		// width-driven cols pick. Mirrors gridLines's "≤ rowsBudget
		// stays single-col" rule so hint registration order reads top-
		// down on terminals with room to spare.
		cols = 1
		rows = len(cells)
	}
	if capacity := cols * rows; len(cells) > capacity {
		cells = cells[:capacity]
	}
	return renderGrid(cells, cols, rows, cellW)
}

// widestCell returns the maximum lipgloss.Width across cells. 0
// for an empty slice.
func widestCell(cells []string) int {
	w := 0
	for _, c := range cells {
		if x := lipgloss.Width(c); x > w {
			w = x
		}
	}
	return w
}

// renderGrid lays cells column-major into `cols × rows`, padding
// each visible cell to cellW so columns line up. Both gridLines
// and gridLinesWithWidth share this row builder once they've
// settled on a (cols, rows, cellW) triple.
func renderGrid(cells []string, cols, rows, cellW int) []string {
	out := make([]string, rows)
	rowCap := cols*cellW + (cols-1)*colGap
	for r := range rows {
		var sb strings.Builder
		sb.Grow(rowCap)
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

// renderHintLines formats the hint column as a k9s-style column-
// major grid of `<key> Description` cells. Mirrors k9s's frame.menu
// zone. Unlike tenants, hints reflow under width pressure: cols
// shrink 3 → 2 → 1, and the trailing chip drops once 1-col still
// won't fit. Pass `unboundedRows` for availWidth to opt out of the
// width-aware reflow (used for the pre-logo-drop pass).
func renderHintLines(hints []action.Action, rowsBudget, availWidth int, styles *theme.Styles) []string {
	if len(hints) == 0 {
		return nil
	}
	maxKey := 0
	for _, a := range hints {
		w := lipgloss.Width(help.ChipText(a.Key))
		if w > maxKey {
			maxKey = w
		}
	}
	descStyle := styles.Hint.DefaultFg
	cells := make([]string, len(hints))
	for i, a := range hints {
		keyStyle := styles.Hint.KeyBold
		if a.Key == "?" {
			keyStyle = styles.Hint.HelpKeyBold
		}
		key := keyStyle.Render(help.ChipText(a.Key))
		pad := strings.Repeat(" ", maxKey-lipgloss.Width(key)+1)
		cells[i] = key + pad + descStyle.Render(a.Description)
	}
	if availWidth >= unboundedRows {
		return gridLines(cells, rowsBudget)
	}
	return gridLinesWithWidth(cells, rowsBudget, availWidth)
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

// MinBodyWidth is the smallest viewport width at which the
// table-based pages render legibly. Below the threshold RenderBody
// substitutes a centred "resize" placeholder so the operator still
// sees the panel chrome and page title. 60 clears known cell-width
// footguns and still fits a half-screen tmux pane.
const MinBodyWidth = 60

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
// When width < MinBodyWidth the body content is replaced by a
// short "terminal too narrow" placeholder so the page does not
// corrupt; the chrome (title + footer) is still rendered so the
// operator sees which view they are on.
//
// Border characters are foreground-tinted with the skin's
// `frame.border.fgColor` (k9s parity); the title text is tinted
// with `frame.title.fgColor`. The inner content is left untouched
// so per-cell colouring inside the body keeps showing through.
func RenderBody(width, height int, body, title, footer string, styles *theme.Styles) string {
	if width < 4 || height < 2 {
		return body
	}
	if width < MinBodyWidth {
		body = narrowPlaceholder(width-2, height-2)
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
			// Body lines arrive pre-styled (e.g. severity-coloured
			// alert names); the SGR-aware clamp keeps escape sequences
			// intact when truncation lands inside one.
			l = format.SGRTruncate(l, innerWidth)
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
func RenderFrame(width int, body string, styles *theme.Styles) string {
	if width < 4 {
		return body
	}
	innerWidth := width - 2
	w := lipgloss.Width(body)
	switch {
	case w > innerWidth:
		body = format.Truncate(body, innerWidth)
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
func buildTitleBorder(innerWidth int, title string, styles *theme.Styles) string {
	return buildLabelBorder(innerWidth, title, "┌", "┐", styles)
}

// buildFooterBorder is the bottom-edge counterpart: same layout,
// `└` / `┘` corners. Empty label renders a plain rule so pages
// without ambient state to surface still get a clean frame.
func buildFooterBorder(innerWidth int, footer string, styles *theme.Styles) string {
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
func buildLabelBorder(innerWidth int, label, leftCorner, rightCorner string, styles *theme.Styles) string {
	border := styles.Frame.Border
	if label == "" {
		return border.Render(leftCorner + strings.Repeat("─", innerWidth) + rightCorner)
	}
	if lipgloss.Width(label)+4 > innerWidth {
		label = format.Truncate(label, innerWidth-4)
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

// narrowPlaceholder returns the body string substituted when the
// viewport is too narrow for normal rendering. innerWidth +
// innerHeight are the dimensions inside the panel chrome
// (RenderBody passes width-2 / height-2). The placeholder picks
// the longest variant that still fits in innerWidth so the
// actionable "resize to >= X cols" hint survives at every width
// the chrome can render at.
//
// Variants in fit order (widest → narrowest):
//
//  1. "terminal too narrow — resize to >= N cols"
//  2. "resize to >= N cols"
//  3. ">= N cols"
//  4. "narrow"  (last-resort sentinel, never useful but keeps
//     the body visibly non-empty when even the cols count
//     cannot fit)
//
// The result is left-justified rather than centered so the
// truncation, when it happens at all, drops the prefix rather
// than the cols count itself.
func narrowPlaceholder(innerWidth, innerHeight int) string {
	variants := []string{
		fmt.Sprintf("terminal too narrow — resize to >= %d cols", MinBodyWidth),
		fmt.Sprintf("resize to >= %d cols", MinBodyWidth),
		fmt.Sprintf(">= %d cols", MinBodyWidth),
		"narrow",
	}
	msg := variants[len(variants)-1]
	for _, v := range variants {
		if lipgloss.Width(v) <= innerWidth {
			msg = v
			break
		}
	}
	if innerHeight <= 0 {
		return msg
	}
	// Center vertically by prepending blank lines; the body slot
	// pads to height with empty strings so any prefix carries.
	pad := max(innerHeight/2, 0)
	lines := make([]string, 0, pad+1)
	for range pad {
		lines = append(lines, "")
	}
	lines = append(lines, msg)
	return strings.Join(lines, "\n")
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

// SPDX-License-Identifier: Apache-2.0

// Package panel renders the k9s-style top panel and bordered
// body. The top panel is a 3-column row: labelled info on the
// left, action shortcuts in the middle, ASCII logo on the right.
// The body wrapper draws a single-line border with the page
// title centred in the top edge.
package panel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

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
// The output is exactly state.Width columns wide.
func RenderTop(state State, styles theme.Styles) string {
	if state.Width <= 0 {
		return ""
	}
	infoLines := renderInfoLines(state.Info, styles)
	tenantLines := renderTenantLines(state.Tenants, styles)
	hintLines := renderHintLines(state.Hints, styles)
	logoLines := splitNonEmpty(state.Logo)

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
		logo := padRight(getLine(logoLines, i), logoW)

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
// `<key> name` table styled with the hint key colour but bolded
// to match k9s's convention of distinguishing tenant / namespace
// shortcuts from action shortcuts.
func renderTenantLines(tenants []TenantBinding, styles theme.Styles) []string {
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
	out := make([]string, len(tenants))
	keyStyle := styles.Hint.HelpKey.Bold(true)
	nameStyle := hintFgOnly(styles).Bold(true)
	for i, t := range tenants {
		key := keyStyle.Render("<" + t.Key + ">")
		pad := strings.Repeat(" ", maxKey-lipgloss.Width(key)+1)
		out[i] = key + pad + nameStyle.Render(t.Name)
	}
	return out
}

// hintFgOnly returns a fresh style carrying only the Hint
// foreground colour. Used for the panel's labels, tenant names,
// and action descriptions so the top panel reads as text on the
// body background rather than a separate dimmed stripe.
func hintFgOnly(styles theme.Styles) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(styles.Hint.Default.GetForeground())
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
	labelStyle := hintFgOnly(styles)
	// Values use the body's foreground only — same fg-only
	// treatment the labels get above. Calling Body.Default would
	// paint the body's bg behind the value, which renders as a
	// stripe of mismatched colour against the surrounding empty
	// cells in the panel zone.
	valueStyle := lipgloss.NewStyle().Foreground(styles.Body.Default.GetForeground())
	for i, l := range lines {
		pad := strings.Repeat(" ", maxLabel-lipgloss.Width(l.Label))
		out[i] = labelStyle.Render(l.Label+":") + pad + " " + valueStyle.Render(l.Value)
	}
	return out
}

// renderHintLines formats the hint column as `<key> Description`
// rows. Mirrors k9s's frame.menu zone.
func renderHintLines(hints []action.Action, styles theme.Styles) []string {
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
	out := make([]string, len(hints))
	descStyle := hintFgOnly(styles)
	for i, a := range hints {
		keyStyle := styles.Hint.Key.Bold(true)
		if a.Key == "?" {
			keyStyle = styles.Hint.HelpKey.Bold(true)
		}
		key := keyStyle.Render("<" + a.Key + ">")
		pad := strings.Repeat(" ", maxKey-lipgloss.Width(key)+1)
		out[i] = key + pad + descStyle.Render(a.Description)
	}
	return out
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
func RenderBody(width, height int, body, title, footer string, styles theme.Styles) string {
	if width < 4 || height < 2 {
		return body
	}
	innerWidth := width - 2
	innerHeight := height - 2

	// Top border with embedded title: "┌── title ──┐".
	top := buildTitleBorder(innerWidth, title)

	// Inner body: split body into lines, pad / slice to fit inner.
	lines := strings.Split(body, "\n")
	if len(lines) > innerHeight {
		lines = lines[:innerHeight]
	}
	for len(lines) < innerHeight {
		lines = append(lines, "")
	}
	for i, l := range lines {
		w := lipgloss.Width(l)
		if w > innerWidth {
			l = truncate(l, innerWidth)
		} else if w < innerWidth {
			l += strings.Repeat(" ", innerWidth-w)
		}
		lines[i] = "│" + l + "│"
	}

	// Bottom border, optionally embedding a label the same way
	// the title sits in the top edge.
	bottom := buildFooterBorder(innerWidth, footer)

	frame := top + "\n" + strings.Join(lines, "\n") + "\n" + bottom
	_ = styles // reserved for the future BorderForeground hook
	return frame
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
// what they see.
func RenderFrame(width int, body string, _ theme.Styles) string {
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
	top := "┌" + strings.Repeat("─", innerWidth) + "┐"
	bottom := "└" + strings.Repeat("─", innerWidth) + "┘"
	return top + "\n│" + body + "│\n" + bottom
}

// buildTitleBorder draws "┌── title ──┐" with the title centred.
// Falls back to a plain border when the title is too long for
// the inner width (rare on terminals ≥ 80 cols).
func buildTitleBorder(innerWidth int, title string) string {
	return buildLabelBorder(innerWidth, title, "┌", "┐")
}

// buildFooterBorder is the bottom-edge counterpart: same layout,
// `└` / `┘` corners. Empty label renders a plain rule so pages
// without ambient state to surface still get a clean frame.
func buildFooterBorder(innerWidth int, footer string) string {
	return buildLabelBorder(innerWidth, footer, "└", "┘")
}

// buildLabelBorder draws "<L>── label ──<R>" with the label
// centred. Falls back to a plain rule when label is empty or
// would not leave at least 2 chars of border on each side.
func buildLabelBorder(innerWidth int, label, leftCorner, rightCorner string) string {
	if label == "" {
		return leftCorner + strings.Repeat("─", innerWidth) + rightCorner
	}
	wrapped := " " + label + " "
	labelW := lipgloss.Width(wrapped)
	if labelW+4 > innerWidth {
		wrapped = " " + truncate(label, innerWidth-4) + " "
		labelW = lipgloss.Width(wrapped)
	}
	left := (innerWidth - labelW) / 2
	right := innerWidth - labelW - left
	return leftCorner + strings.Repeat("─", left) + wrapped + strings.Repeat("─", right) + rightCorner
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

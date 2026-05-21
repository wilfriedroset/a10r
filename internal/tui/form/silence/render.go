// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// labelWidth is the column width reserved for the field labels
// (`Matchers:`, `Starts:`, …). Eleven cols fit the longest label
// plus the colon plus a space.
const labelWidth = 11

// enterToChangeHint is the picker-affordance label echoed both in
// the tenant row's inline suffix and (canonically) in Bindings().
// Sharing the literal keeps the two sites in lockstep so a future
// rename can't leave one stale.
const enterToChangeHint = "[Enter to change]"

// renderView is the body of Form.View. Lives on render.go so the
// view helpers (tenantRow / disabledRow / fieldRow / …) sit next
// to it; Form.View itself stays in form.go as a thin delegate
// because it's part of the app.Page contract.
//
// Renders one labeled row per field — label on the left, the
// bubbles input's View on the right — with the focused row's
// label tinted via the theme's accent colour and a leading `▸`
// so the active field is unmissable.
//
// Row order per ADR-0011: Tenant first (omitted in bulk; read-only
// when single-client or in edit mode), then Matchers / Targets,
// then the metadata fields.
func (f *Form) renderView(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	inputWidth := max(
		// -2 = leading prefix "▸ " or "  "
		width-labelWidth-2, 10)
	f.matchers.SetWidth(inputWidth)
	f.starts.SetWidth(inputWidth)
	f.ends.SetWidth(inputWidth)
	f.creator.SetWidth(inputWidth)
	f.comment.SetWidth(inputWidth)

	rows := make([]string, 0, int(numFields))
	if !f.bulk {
		rows = append(rows, f.tenantRow(inputWidth))
	}
	rows = append(rows,
		f.matcherSlotRow(),
		f.fieldRow("Starts", fieldStarts, f.starts.View()),
		f.fieldRow("Ends", fieldEnds, f.ends.View()),
		f.fieldRow("Creator", fieldCreator, f.creator.View()),
		f.fieldRow("Comment", fieldComment, f.comment.View()),
	)
	body := strings.Join(rows, "\n")
	if f.err != "" {
		// The hint strip in the top panel already advertises
		// Tab / Shift+Tab / Ctrl+S; the only thing the form
		// itself needs to surface in the body is a recent
		// validation error so the user can see what to fix.
		body += "\n\n" + f.styles.Flash.Error.Render("ERR: "+f.err)
	}
	return lipgloss.NewStyle().Width(width).Render(body)
}

// tenantRow renders the leading Tenant row. The value is the
// current f.tenant (empty string falls back to a "—" placeholder
// so an unselected form is still visually obvious). When the row
// is disabled — single-client deployments and edit mode — the
// renderer drops the leading `▸` marker even when focus happens
// to land here, and falls back to the neutral label style so the
// row reads as informational rather than actionable. Bulk mode
// never reaches this code path (View omits the row outright).
func (f *Form) tenantRow(inputWidth int) string {
	value := f.tenant
	if value == "" {
		value = "—"
	}
	if f.tenantDisabled() {
		return f.disabledRow("Tenant", value)
	}
	// Append a faint "[Enter to change]" hint so the picker affordance
	// is discoverable without the user having to guess. Faint
	// (`\x1b[2m`) is foreground-only — no background paint — and sits
	// next to the value so it doesn't clutter the focus marker. The
	// hint is unconditional (focused or blurred) so a user scanning
	// the form learns the affordance before ever tabbing onto the row.
	//
	// Elided when the row would otherwise wrap: with a long tenant
	// name on a narrow form, appending the hint would push the line
	// past inputWidth and the outer View's Width-wrap would break
	// fieldRow's grid alignment. Trade discoverability for layout
	// integrity at narrow widths — Bindings() still advertises the
	// affordance in the global hint strip.
	const hintBody = enterToChangeHint
	hintCols := lipgloss.Width("  ") + lipgloss.Width(hintBody)
	if lipgloss.Width(value)+hintCols > inputWidth {
		return f.fieldRow("Tenant", fieldTenant, value)
	}
	hint := lipgloss.NewStyle().Faint(true).Render("  " + hintBody)
	return f.fieldRow("Tenant", fieldTenant, value+hint)
}

// disabledRow renders a read-only row with the value dimmed via
// lipgloss.Faint (SGR `\x1b[2m`). Foreground-only by definition so
// the no-bg-paint rule still holds; ADR-0011 calls for the dim
// treatment so a disabled row reads differently from a blurred-
// but-interactive one.
func (f *Form) disabledRow(label, value string) string {
	prefix := "  "
	labelStyle := lipgloss.NewStyle().
		Foreground(f.styles.Body.Default.GetForeground()).
		Bold(true)
	labelText := labelStyle.Render(format.PadRight(label+":", labelWidth))
	valueStyle := lipgloss.NewStyle().Faint(true)
	lines := strings.Split(value, "\n")
	for i, ln := range lines {
		dimmed := valueStyle.Render(ln)
		if i == 0 {
			lines[i] = prefix + labelText + dimmed
		} else {
			lines[i] = strings.Repeat(" ", 2+labelWidth) + dimmed
		}
	}
	return strings.Join(lines, "\n")
}

// matcherSlotRow renders the top row of the form. In create / edit
// mode this is the live matchers textarea labelled "Matchers"; in
// bulk mode the textarea is hidden and the slot is filled with the
// non-focusable BulkBanner labelled "Targets" — the banner carries
// the count breakdown the user needs to see what their submit will
// fan out to.
func (f *Form) matcherSlotRow() string {
	if f.bulk {
		return f.fieldRow("Targets", fieldMatchers, f.bulkBanner)
	}
	return f.fieldRow("Matchers", fieldMatchers, f.matchersView())
}

// matchersView wraps f.matchers.View() to work around a bubbles
// textarea bug: placeholderView (textarea.go:1513) only wraps the
// FIRST line of a multi-line Placeholder with the placeholder
// style; lines 2..N render with cursorLine only, which
// flattenTextareaBlur sets to bare — leaving them at the
// terminal's default foreground while line 1 is dim. The result
// is a multi-line hint whose continuation lines visually read as
// typed text. Compose around upstream (no-fork): when the buffer
// is empty, re-style the trailing placeholder lines so the full
// hint reads as one placeholder.
func (f *Form) matchersView() string {
	raw := f.matchers.View()
	if f.matchers.Value() != "" {
		return raw
	}
	if !strings.Contains(f.matchers.Placeholder, "\n") {
		return raw
	}
	// Replicate bubbles' placeholder wrap (textarea.go:1521-1525) so
	// our `plines` matches what bubbles actually rendered — anchoring
	// against the raw `Placeholder` field's newline-split would miss
	// at narrow widths where bubbles word/hard-wraps a long line
	// before splitting, and the substring index would return -1.
	width := f.matchers.Width()
	pwrap := ansi.Hardwrap(ansi.Wordwrap(f.matchers.Placeholder, width, ""), width, true)
	plines := strings.Split(strings.TrimSpace(pwrap), "\n")
	if len(plines) <= 1 {
		return raw
	}
	styles := f.matchers.Styles()
	state := styles.Blurred
	if f.matchers.Focused() {
		state = styles.Focused
	}
	dim := state.Placeholder.Inherit(state.Base).Inline(true)
	lines := strings.Split(raw, "\n")
	for i := 1; i < len(lines) && i < len(plines); i++ {
		phLine := plines[i]
		// bubbles wraps every line with an empty-render prefix
		// (cursor/prompt SGR pair) before the actual placeholder
		// text, so anchor by substring rather than full-line
		// equality. Rewrite the placeholder text in place; the
		// surrounding SGR padding stays as bubbles emitted it.
		idx := strings.Index(lines[i], phLine)
		if idx < 0 {
			continue
		}
		lines[i] = lines[i][:idx] + dim.Render(phLine) + lines[i][idx+len(phLine):]
	}
	return strings.Join(lines, "\n")
}

// fieldRow assembles one labelled row: leading prefix (▸ for the
// focused field, two spaces otherwise) + padded label + the
// bubbles input's already-rendered View. Multi-line input values
// get the label only on the first row; continuation lines align
// under the input column so a multi-line matchers buffer reads
// as one block visually.
//
// Labels are rendered foreground-only and bold. Body.Default
// carries a background colour for the page chrome — painting it
// behind every label would draw a stripe that doesn't match the
// inputs alongside, so its foreground is extracted explicitly.
// Header.Accent is already foreground-only per the theme spec
// but isn't bold; Bold(true) is a real apply on both branches so
// labels read as row headers regardless of focus state, while
// Header.Accent's yellow singles out the active row.
func (f *Form) fieldRow(label string, idx fieldIndex, rendered string) string {
	prefix := "  "
	labelStyle := lipgloss.NewStyle().
		Foreground(f.styles.Body.Default.GetForeground()).
		Bold(true)
	if idx == f.focus {
		prefix = "▸ "
		labelStyle = f.styles.Header.Accent.Bold(true)
	}
	labelText := labelStyle.Render(format.PadRight(label+":", labelWidth))
	lines := strings.Split(rendered, "\n")
	for i, ln := range lines {
		if i == 0 {
			lines[i] = prefix + labelText + ln
		} else {
			lines[i] = strings.Repeat(" ", 2+labelWidth) + ln
		}
	}
	return strings.Join(lines, "\n")
}

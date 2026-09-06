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

// endsHintSuffix is the Duration shorthand cue appended to the
// Ends row's input. The `m=min` half names the documented footgun
// (single-letter `m` is minute, never month — see ADR 0034) so
// the disambiguation cue stays on screen for the whole edit, not
// just the placeholder slot. Mirrors the tenant row's inline
// affordance pattern.
const endsHintSuffix = "7d · 1w2d · m=min"

// endsHintGap is the column gap between the value's fixed column
// slot (endsValueReserve) and the leading edge of the hint suffix.
// Three cols cover the cursor slot plus two cols of breathing room
// so the hint never butts up against typed text. The gap is added
// to a FIXED anchor, not the live value width, so the suffix holds
// its column as the operator types (see endsRow for the jitter
// rationale).
const endsHintGap = 3

// endsValueReserve is the fixed column slot held for the value
// while the duration cue is shown. Eight cols hold the longest
// realistic duration shorthand (`1w2d3h4m` = 8 cols), so the cue
// stays put across a normal duration edit instead of sliding right
// per keystroke. It also sits comfortably below the ~20-col
// RFC3339 timestamp floor, so a timestamp value trips the elision
// branch and reclaims the full input width — correct, not a
// regression, since the duration cue is meaningless once the
// operator is typing a timestamp.
const endsValueReserve = 8

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
		width-labelWidth-2, 10,
	)
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
		f.endsRow(inputWidth),
		f.fieldRow("Creator", fieldCreator, f.creator.View()),
		f.fieldRow("Comment", fieldComment, f.comment.View()),
	)
	body := strings.Join(rows, "\n")
	if f.scopeNote != "" {
		// Persistent scope banner at the very top so the operator
		// reads the blast radius before the matchers. Foreground-only
		// (warn tint) keeps the chrome-on-default-bg rule; the outer
		// Width wrap below folds a long note across lines. Rendered
		// here rather than as a field row so it never enters the Tab
		// focus cycle (fields.go is untouched).
		note := lipgloss.NewStyle().
			Foreground(f.styles.Flash.Warn.GetForeground()).
			Bold(true).
			Render(f.scopeNote)
		body = note + "\n\n" + body
	}
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

// endsRow renders the Ends row with the Duration shorthand hint
// suffix anchored at a FIXED column. The suffix starts at
// endsValueReserve+endsHintGap regardless of the live value width,
// so it holds its column as the operator types — fixing a reported
// per-keystroke jitter where anchoring on the value's right edge
// slid the cue right on every key. The trade is a small detached
// gap on a short value in exchange for a stable column; for a cue
// the eye learns to ignore once read, a still column beats a close
// one.
//
// Once the value outgrows the shorthand range (width >
// endsValueReserve) the operator is no longer typing a duration —
// in practice they're entering an RFC3339 timestamp, for which the
// duration cue is meaningless. Eliding it there and handing the
// input the full width (renderView already sized it to inputWidth)
// is correct, not a regression: it lets the ~25-col timestamp show
// in full.
//
// The narrow-width guard stays: if the fixed anchor plus the suffix
// can't fit inputWidth, drop the cue so fieldRow's grid alignment
// holds. Only the Ends row uses this treatment — the cue is
// load-bearing for the `1m`-thinking-month footgun and irrelevant
// to Starts / Creator / Comment.
func (f *Form) endsRow(inputWidth int) string {
	if lipgloss.Width(f.ends.Value()) > endsValueReserve {
		return f.fieldRow("Ends", fieldEnds, f.ends.View())
	}
	suffixStart := endsValueReserve + endsHintGap
	if suffixStart+lipgloss.Width(endsHintSuffix) > inputWidth {
		return f.fieldRow("Ends", fieldEnds, f.ends.View())
	}
	// Bubbles' textinput reserves one extra column past SetWidth
	// for the cursor slot, so rendered width is one wider than
	// what we pass. Shrink to suffixStart-1 so the rendered input
	// lands at exactly suffixStart cols and the suffix concatenates
	// at the intended position.
	f.ends.SetWidth(suffixStart - 1)
	hint := lipgloss.NewStyle().Faint(true).Render(endsHintSuffix)
	return f.fieldRow("Ends", fieldEnds, f.ends.View()+hint)
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

// SPDX-License-Identifier: Apache-2.0

// Package header renders the three-zone header strip:
//
//	[ tenants: prod ● 142 · 5s ]   [ per-view content ]   [ hints  [s] silence  [?] help ]
//
// Stateless: pages build a State and call Render. The app shell
// is responsible for keeping conn-state aggregated across backends
// and re-rendering on every BackendStatusMsg / poll tick; the
// header just displays what it is told.
package header

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// ConnState is the aggregated per-session connection state. When
// multiple tenants are selected, the upstream caller computes the
// worst-case across them; the header renders one indicator.
type ConnState int

// Connection states ordered worst-last so callers can max() across
// backends: connected (●), degraded (◐), unreachable (○).
const (
	ConnConnected ConnState = iota
	ConnDegraded
	ConnUnreachable
)

// String returns the ●/◐/○ rune for the connection state.
// Exported so other packages (status pane, tenant table) can
// surface the same indicator without re-implementing the mapping.
func (s ConnState) String() string {
	switch s {
	case ConnConnected:
		return "●"
	case ConnDegraded:
		return "◐"
	case ConnUnreachable:
		return "○"
	}
	return "?"
}

// State is the rendering input for the header strip. Stateless:
// callers reconstruct it on every frame from their own state.
type State struct {
	// Tenants is the tenant-selection label: "prod",
	// "prod, staging", or "all (3)". Empty renders as "tenants:".
	Tenants string

	// Conn is the aggregated connection indicator.
	Conn ConnState

	// Count is rendered next to the indicator (e.g. "142 alerts").
	// Empty omits the count + middle dot.
	Count string

	// Age is rendered after the count (e.g. "5s ago"). Empty omits
	// the age + middle dot.
	Age string

	// Content is the per-view content slot text. Truncated with
	// "…+K more" when it exceeds the middle zone's width budget.
	Content string

	// Hints are the action.Action slice for the active view (already
	// filtered by read-only mode at the registry layer). Rendered
	// right-aligned as `[k] desc  [k] desc  [?] help`.
	Hints []action.Action

	// Width is the terminal column count; the renderer expands or
	// truncates the middle zone to fit.
	Width int
}

// Truncation marker shown when middle-zone content overflows.
// Single non-ASCII rune so we don't have to chase a width=4 ASCII
// alternative if the terminal is narrow.
const truncationMarker = "…"

// minMiddleWidth is the floor below which the middle zone is
// dropped entirely rather than rendered as a meaningless `…`.
const minMiddleWidth = 4

// middleBreathingCols is the gap reserved between the middle zone
// and the right zone so they don't visually run together when the
// content slot is filled.
const middleBreathingCols = 2

// MinSensibleWidth is the floor below which the renderer no longer
// guarantees the width-padding invariant. The left zone's required
// content (tenants + glyph + count + age) can itself exceed very
// small widths, and ANSI-aware truncation of styled left-zone
// output is intentionally out of scope. Pages should never see a
// tea.WindowSizeMsg below this width on any modern terminal.
const MinSensibleWidth = 50

// Render produces the styled header string for state. Returns
// width-padded output exactly state.Width columns wide so the
// caller can paste it into a vertical layout without further
// alignment — provided state.Width >= MinSensibleWidth. At narrower
// widths the right zone shrinks (dropping trailing hints, never
// the left zone), so output may exceed state.Width when the left
// zone's required content alone is wider.
//
// All zones render foreground-only so the strip stays flush with
// the surrounding chrome (which lets the terminal default bg show
// through). Painting `Header.Default`'s palette bg behind the line
// would draw a coloured stripe inside an otherwise transparent
// frame — same trap that bit the `:` / `/` prompt before.
func Render(state State, styles *theme.Styles) string {
	left := renderLeft(state, styles)
	leftWidth := lipgloss.Width(left)

	// Bound the right zone so the total stays within state.Width.
	// Drop trailing hints (least important per registration order)
	// until the strip fits the budget; the `?` help action — which
	// pages register last — survives drops longest.
	rightBudget := max(state.Width-leftWidth-minMiddleWidth, 0)
	right := renderHintsWithBudget(state.Hints, rightBudget, styles)
	rightWidth := lipgloss.Width(right)

	middleBudget := state.Width - leftWidth - rightWidth - middleBreathingCols
	middle := renderMiddle(state.Content, middleBudget, styles)
	middleWidth := lipgloss.Width(middle)

	gap := max(state.Width-leftWidth-middleWidth-rightWidth, 0)
	spacer := strings.Repeat(" ", gap)

	return left + middle + spacer + right
}

// renderHintsWithBudget formats the hint strip and drops trailing
// entries until it fits the budget. Pages should register the most
// important affordances first so the drop-from-end strategy keeps
// the highest-priority hints visible at narrow widths.
func renderHintsWithBudget(hints []action.Action, budget int, styles *theme.Styles) string {
	if len(hints) == 0 || budget <= 0 {
		return ""
	}
	current := hints
	for len(current) > 0 {
		out := renderHints(current, styles)
		if lipgloss.Width(out) <= budget {
			return out
		}
		current = current[:len(current)-1]
	}
	return ""
}

// renderLeft formats the tenant indicator + glyph + count + age.
func renderLeft(state State, styles *theme.Styles) string {
	var b strings.Builder
	fg := styles.Header.DefaultFg

	b.WriteString(fg.Render("tenants: "))
	if state.Tenants != "" {
		b.WriteString(styles.Header.Accent.Render(state.Tenants))
	}
	b.WriteString(" ")
	b.WriteString(connStyle(state.Conn, styles).Render(state.Conn.String()))

	if state.Count != "" {
		b.WriteString(fg.Render(" · "))
		b.WriteString(fg.Render(state.Count))
	}
	if state.Age != "" {
		b.WriteString(fg.Render(" · "))
		b.WriteString(fg.Render(state.Age))
	}
	return b.String()
}

// connStyle picks the lipgloss style matching the connection state.
// All four branches return foreground-only styles: OK / Warn /
// Error are foreground-only per the theme spec, and the Default
// fall-through goes through theme.FgOnly so it doesn't paint a
// palette bg.
func connStyle(c ConnState, styles *theme.Styles) lipgloss.Style {
	switch c {
	case ConnConnected:
		return styles.Header.OK
	case ConnDegraded:
		return styles.Header.Warn
	case ConnUnreachable:
		return styles.Header.Error
	}
	return styles.Header.DefaultFg
}

// renderMiddle truncates content to fit budget columns. Returns
// empty string when budget is below the minimum sensible width.
func renderMiddle(content string, budget int, styles *theme.Styles) string {
	if content == "" || budget < minMiddleWidth {
		return ""
	}
	fg := styles.Header.DefaultFg
	if lipgloss.Width(content) <= budget {
		return fg.Render(content)
	}
	// Truncate to budget-1 columns and append the marker.
	truncated := format.Truncate(content, budget-lipgloss.Width(truncationMarker))
	return fg.Render(truncated + truncationMarker)
}

// renderHints formats the right-aligned hint strip from the
// supplied actions. The `?` help action gets the help_key colour;
// other shortcuts get the regular key colour. Descriptions are
// foreground-only (Hint.Default carries fg+bg, but the strip sits
// in unstyled chrome — see Render's docstring).
func renderHints(hints []action.Action, styles *theme.Styles) string {
	if len(hints) == 0 {
		return ""
	}
	descStyle := styles.Hint.DefaultFg
	var b strings.Builder
	for i, a := range hints {
		if i > 0 {
			b.WriteString("  ")
		}
		keyStyle := styles.Hint.Key
		if a.Key == "?" {
			keyStyle = styles.Hint.HelpKey
		}
		b.WriteString(keyStyle.Render("[" + a.Key + "]"))
		if a.Description != "" {
			b.WriteString(" ")
			b.WriteString(descStyle.Render(a.Description))
		}
	}
	return b.String()
}

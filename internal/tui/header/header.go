// SPDX-License-Identifier: Apache-2.0

// Package header renders the J1 three-zone header strip:
//
//	[ tenants: prod ● 142 · 5s ]   [ per-view content ]   [ hints  [s] silence  [?] help ]
//
// Stateless: pages build a State and call Render. The app shell
// (#22) is responsible for keeping conn-state aggregated across
// backends and re-rendering on every BackendStatusMsg / poll tick;
// per C2 / C3 the header just displays what it is told.
package header

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// ConnState is the aggregated per-session connection state per C2.
// When multiple tenants are selected, the upstream caller computes
// the worst-case across them; the header renders one indicator.
type ConnState int

// Connection states in C2 order: connected (●), degraded (◐),
// unreachable (○). The numeric ordering matches "worsening" so
// callers can max() across backends.
const (
	ConnConnected ConnState = iota
	ConnDegraded
	ConnUnreachable
)

// glyph returns the ●/◐/○ rune for the connection state.
func (s ConnState) glyph() string {
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
	// Tenants is the tenant-selection label (per C3): "prod",
	// "prod, staging", or "all (3)". Empty renders as "tenants:".
	Tenants string

	// Conn is the aggregated connection indicator per C2.
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
	// filtered by C4 read-only mode at the registry layer). Rendered
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

// Render produces the styled header string for state. Returns
// width-padded output exactly state.Width columns wide so the
// caller can paste it into a vertical layout without further
// alignment.
func Render(state State, styles theme.Styles) string {
	left := renderLeft(state, styles)
	right := renderHints(state.Hints, styles)

	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	middleBudget := state.Width - leftWidth - rightWidth - 2 // 2 cols of breathing room
	middle := renderMiddle(state.Content, middleBudget, styles)
	middleWidth := lipgloss.Width(middle)

	gap := max(state.Width-leftWidth-middleWidth-rightWidth, 0)
	spacer := strings.Repeat(" ", gap)

	return styles.Header.Default.Render(left + middle + spacer + right)
}

// renderLeft formats the tenant indicator + glyph + count + age.
func renderLeft(state State, styles theme.Styles) string {
	var b strings.Builder

	b.WriteString(styles.Header.Default.Render("tenants: "))
	if state.Tenants != "" {
		b.WriteString(styles.Header.Accent.Render(state.Tenants))
	}
	b.WriteString(" ")
	b.WriteString(connStyle(state.Conn, styles).Render(state.Conn.glyph()))

	if state.Count != "" {
		b.WriteString(styles.Header.Default.Render(" · "))
		b.WriteString(styles.Header.Default.Render(state.Count))
	}
	if state.Age != "" {
		b.WriteString(styles.Header.Default.Render(" · "))
		b.WriteString(styles.Header.Default.Render(state.Age))
	}
	return b.String()
}

// connStyle picks the lipgloss style matching the connection state.
func connStyle(c ConnState, styles theme.Styles) lipgloss.Style {
	switch c {
	case ConnConnected:
		return styles.Header.OK
	case ConnDegraded:
		return styles.Header.Warn
	case ConnUnreachable:
		return styles.Header.Error
	}
	return styles.Header.Default
}

// renderMiddle truncates content to fit budget columns. Returns
// empty string when budget is below the minimum sensible width.
func renderMiddle(content string, budget int, styles theme.Styles) string {
	if content == "" || budget < minMiddleWidth {
		return ""
	}
	if lipgloss.Width(content) <= budget {
		return styles.Header.Default.Render(content)
	}
	// Truncate to budget-1 columns and append the marker.
	truncated := truncate(content, budget-lipgloss.Width(truncationMarker))
	return styles.Header.Default.Render(truncated + truncationMarker)
}

// truncate cuts s to at most n columns. Lipgloss-aware width so a
// future emoji content slot doesn't clip on a fractional rune.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	// Walk runes, accumulating width until we hit the limit.
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > n {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// renderHints formats the right-aligned hint strip from the
// supplied actions. The `?` help action gets the help_key colour;
// other shortcuts get the regular key colour.
func renderHints(hints []action.Action, styles theme.Styles) string {
	if len(hints) == 0 {
		return ""
	}
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
			b.WriteString(styles.Hint.Default.Render(a.Description))
		}
	}
	return b.String()
}

// FormatAge is a small helper pages use to compute the "5s ago"
// string for State.Age. Pulled out so the format is consistent
// across pages (alerts, silences) and easy to test.
func FormatAge(now, last time.Time) string {
	if last.IsZero() {
		return ""
	}
	d := now.Sub(last)
	if d < time.Second {
		return "now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

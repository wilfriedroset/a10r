// SPDX-License-Identifier: Apache-2.0

// Package help renders the k9s-style help overlay opened by `?`.
// The view is a bordered box titled "Help" hosting four columns:
//
//		RESOURCE   |  GENERAL   |  NAVIGATION  |  HOTKEYS
//
//	  - RESOURCE lists the active page's verbs plus the global
//	    numeric tenant quick-switch (`<0>` all, `<1>`-`<9>` per
//	    configured backend) — the one column that actually depends
//	    on what the user is looking at.
//	  - GENERAL is the curated globals catalogue (`:` `/` `?` `Esc`
//	    `q` `Ctrl+C` `Ctrl+T` `r`).
//	  - NAVIGATION is the table-context vim motions (`j` `k` `gg`
//	    `G` `Ctrl+D` `Ctrl+U` `Ctrl+F` `Ctrl+B` `h` `l`).
//	  - HOTKEYS holds page-specific sort and filter shortcuts when
//	    the active page exposes them; empty when the page doesn't.
//
// Read-only mode (C4) hides every Dangerous binding from RESOURCE.
package help

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Options bundles every input the overlay needs. The wiring layer
// in app.go assembles this on `?` press.
type Options struct {
	// PageName is the active page's Crumb() — labels the RESOURCE
	// column heading and is shown in the title strip.
	PageName string

	// PageBindings is the active page's Bindings() output. Auto-
	// split inside the modal: shortcuts whose Key begins with
	// "Shift+" land in the HOTKEYS column (sort/filter shortcuts
	// match the k9s shape) and everything else lands in RESOURCE.
	// Both halves go through the ReadOnly filter.
	PageBindings []action.Action

	// Globals is the curated `keybindings.md §Global` list. The
	// App owns this list because the help overlay shouldn't re-
	// derive globals from the dispatcher (the dispatcher stores
	// handlers, not descriptions).
	Globals []action.Action

	// TableMotions is the curated table-context vim-motion list.
	// Same rationale as Globals.
	TableMotions []action.Action

	// Tenants is the configured backend names (in `backends:`
	// order) so the RESOURCE column can render `<1> primary`,
	// `<2> secondary`, ... alongside the global numeric handlers.
	Tenants []string

	// ReadOnly hides every Dangerous binding from the rendered
	// RESOURCE column per C4.
	ReadOnly bool

	// Styles is the compiled theme. Used for the column headers,
	// key chips, and the bordered frame.
	Styles theme.Styles
}

// Help is the modal overlay.
type Help struct {
	opts Options
}

// New constructs a Help modal.
func New(opts Options) *Help { return &Help{opts: opts} }

// Init implements modal.Modal.
func (*Help) Init() tea.Cmd { return nil }

// Title implements modal.Modal — the App's outer panel renders
// this in its border so the help overlay reads as `┌── Help ──┐`
// without needing to draw its own frame.
func (*Help) Title() string { return "Help" }

// Update implements modal.Modal. Any keystroke dismisses the
// overlay — it's read-only so there's no other useful action.
func (h *Help) Update(msg tea.Msg) (modal.Modal, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return h, func() tea.Msg { return modal.HelpClosedMsg{} }
	}
	return h, nil
}

// View implements modal.Modal. Renders the four columns into the
// rectangle the App panel hands over. The outer panel border (with
// the "Help" title) is drawn by the App, not by this view.
func (h *Help) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	cols := h.columns()
	colWidth := max(width/len(cols), 12)

	rows := h.composeRows(cols, colWidth, height)
	return strings.Join(rows, "\n")
}

// columns builds the four column bodies (heading + entry list each)
// in display order. Each entry is already styled so the row joiner
// can pad without re-measuring.
func (h *Help) columns() [][]string {
	verbs, hotkeys := splitVerbsHotkeys(h.opts.PageBindings, h.opts.ReadOnly)
	return [][]string{
		h.resourceColumn(verbs),
		h.staticColumn("GENERAL", h.opts.Globals),
		h.staticColumn("NAVIGATION", h.opts.TableMotions),
		h.staticColumn("HOTKEYS", hotkeys),
	}
}

// resourceColumn lists the tenant numeric quick-switch (`<0>` all,
// `<1>` … `<9>` per configured backend) followed by the active
// page's non-sort verbs. Empty backends just drop the numeric block.
func (h *Help) resourceColumn(verbs []action.Action) []string {
	heading := h.headingLabel("RESOURCE")
	out := []string{heading}

	if len(h.opts.Tenants) > 0 {
		out = append(out, h.entry("0", "all"))
		for i, name := range h.opts.Tenants {
			if i >= 9 {
				break
			}
			out = append(out, h.entry(itoa(i+1), name))
		}
	}

	for _, a := range verbs {
		out = append(out, h.entry(a.Key, a.Description))
	}
	return out
}

// splitVerbsHotkeys partitions the page's bindings into RESOURCE-
// column verbs (mark / drill / silence / filter / state-cycle) and
// HOTKEYS-column shortcuts (sort columns, anything with a `Shift+`
// prefix). Read-only filters Dangerous out of both halves.
func splitVerbsHotkeys(in []action.Action, readOnly bool) (verbs, hotkeys []action.Action) {
	for _, a := range filterDangerous(in, readOnly) {
		if strings.HasPrefix(a.Key, "Shift+") {
			hotkeys = append(hotkeys, a)
			continue
		}
		verbs = append(verbs, a)
	}
	return verbs, hotkeys
}

// staticColumn renders a heading + a list of pre-curated actions.
// Globals / table motions / page hotkeys all flow through here.
func (h *Help) staticColumn(name string, entries []action.Action) []string {
	filtered := filterDangerous(entries, h.opts.ReadOnly)
	out := make([]string, 0, len(filtered)+1)
	out = append(out, h.headingLabel(name))
	for _, a := range filtered {
		out = append(out, h.entry(a.Key, a.Description))
	}
	return out
}

// composeRows zips columns into rows. Short columns are right-
// padded so the cell alignment stays clean across the visible
// rectangle. Each row is exactly colWidth*len(cols) columns wide;
// the App panel takes care of side borders.
func (h *Help) composeRows(cols [][]string, colWidth, height int) []string {
	maxLen := 0
	for _, c := range cols {
		if len(c) > maxLen {
			maxLen = len(c)
		}
	}
	if maxLen > height {
		maxLen = height
	}

	rows := make([]string, 0, maxLen)
	for r := range maxLen {
		var b strings.Builder
		for _, col := range cols {
			cell := ""
			if r < len(col) {
				cell = col[r]
			}
			b.WriteString(padRight(cell, colWidth))
		}
		rows = append(rows, b.String())
	}
	return rows
}

// entry renders one binding as "<key>  description" with the key
// chip styled as a hint helper key (theme.Hint.HelpKey).
func (h *Help) entry(key, desc string) string {
	chip := h.opts.Styles.Hint.HelpKey.Render("<" + key + ">")
	return chip + "  " + desc
}

// headingLabel renders a column heading in the table-header colour
// (uppercase per the v0.1 polish UX rules).
func (h *Help) headingLabel(name string) string {
	st := lipgloss.NewStyle().
		Foreground(h.opts.Styles.Table.Header.GetForeground()).
		Bold(true)
	return st.Render(name)
}

// filterDangerous strips Dangerous-flagged actions when readOnly is
// true; otherwise returns the slice unchanged. Thin wrapper around
// action.FilterDangerous so the existing tests keep their call shape.
func filterDangerous(in []action.Action, readOnly bool) []action.Action {
	if !readOnly {
		return in
	}
	return action.FilterDangerous(in)
}

// padRight pads s with trailing spaces so it occupies exactly w
// columns, lipgloss-aware so styled chips don't blow the math.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	cur := lipgloss.Width(s)
	if cur >= w {
		return truncateVisible(s, w)
	}
	return s + strings.Repeat(" ", w-cur)
}

// truncateVisible cuts s to at most w visible columns. Lipgloss-
// aware to keep ANSI sequences intact.
func truncateVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
		default:
			rw := lipgloss.Width(string(r))
			if used+rw > w {
				return b.String()
			}
			b.WriteRune(r)
			used += rw
		}
	}
	return b.String()
}

// itoa is a small allocation-free int → string for the numeric
// tenant quick-switch (1-9 only).
func itoa(n int) string {
	if n < 0 || n > 9 {
		return ""
	}
	return string(rune('0' + n))
}

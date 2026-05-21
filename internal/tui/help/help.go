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
// Read-only mode hides every Dangerous binding from RESOURCE.
package help

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// ClosedMsg is emitted when the help overlay is dismissed. The app
// shell clears its help slot on receipt. Lives in this package
// (per ADR 0020) because the help overlay owns its own routing
// slot — viewer overlays no longer rent modal/'s ResultMsg surface
// to ship a no-payload marker.
type ClosedMsg struct{}

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

	// Globals is the `keybindings.md §Global` list rendered in the
	// GENERAL column. The App derives this from the dispatcher via
	// `Dispatcher.Bindings(LayerGlobal)` per ADR 0019, with the
	// `r` (refresh) row appended manually because refresh is
	// documented-as-global but implemented-per-page.
	Globals []action.Action

	// TableMotions is the curated table-context vim-motion list.
	// Same rationale as Globals.
	TableMotions []action.Action

	// Tenants is the configured backend names (in `backends:`
	// order) so the RESOURCE column can render `<1> primary`,
	// `<2> secondary`, ... alongside the global numeric handlers.
	Tenants []string

	// ReadOnly hides every Dangerous binding from the rendered
	// RESOURCE column.
	ReadOnly bool

	// Styles is the compiled theme. Used for the column headers,
	// key chips, and the bordered frame.
	Styles *theme.Styles
}

// Help is the viewer overlay rendered by `?`. The app shell holds
// it in a dedicated routing slot (`a.help`) separate from the modal
// slot; the two never render simultaneously.
type Help struct {
	opts Options

	// scroll is the row index the help body starts rendering from.
	// Mouse-wheel ticks and the vim-style scroll keys (j/k/g/G plus
	// PgDn/PgUp/Home/End/Ctrl+D/Ctrl+U/Ctrl+F/Ctrl+B) adjust this
	// so a help payload that overflows the overlay's height is
	// reachable from the keyboard or the wheel. Clamped inside View
	// to whatever the rendered content can show; the scroll-key
	// handler also clamps to lastMaxScroll so a held-down key
	// stops moving the offset once the bottom is reached.
	scroll int

	// lastBodyHeight / lastMaxScroll mirror the View-side clamp
	// inputs so the scroll-key handler can step by half / full page
	// without re-measuring columns. View writes both fields on every
	// render; before the first render they default to zero, which
	// makes every scroll-key press a no-op (correct — there's
	// nothing on screen yet to scroll).
	lastBodyHeight int
	lastMaxScroll  int
}

// New constructs a Help overlay.
func New(opts Options) *Help { return &Help{opts: opts} }

// Update handles the keystrokes and wheel ticks the app shell
// routes here while the overlay is open. Most keys dismiss
// (it's read-only — `?` toggles off, `Esc` and `q` close it),
// but the standard vim-style scroll keys (j/k/g/G/Ctrl+D/Ctrl+U/
// Ctrl+F/Ctrl+B plus the arrow / page-nav keys and Space) walk
// the scroll offset instead. Wheel-only scrolling is
// undiscoverable — a user reflexively pressing j/k to scroll a
// long help body would otherwise close the overlay on the first
// keystroke. Click / motion events arrive only while the App's
// mouse cell-motion mode is on but the help overlay has no use
// for them — they're ignored alongside other non-key messages.
func (h *Help) Update(msg tea.Msg) (*Help, tea.Cmd) {
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		h.scrollBy(wheel)
		return h, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if h.scrollByKey(key.String()) {
			return h, nil
		}
		return h, func() tea.Msg { return ClosedMsg{} }
	}
	return h, nil
}

// scrollBy adjusts the scroll offset for a wheel tick. Up reduces
// the offset; down increases it. The lower bound is zero; the
// upper bound is enforced inside View so a window resize doesn't
// strand the offset past the new maximum (the scroll field is the
// only mutable state on the help overlay — re-clamping there keeps
// the math centralised).
func (h *Help) scrollBy(m tea.MouseWheelMsg) {
	switch m.Button {
	case tea.MouseWheelUp:
		if h.scroll > 0 {
			h.scroll--
		}
	case tea.MouseWheelDown:
		h.scroll++
	}
}

// scrollByKey routes the vim-style scroll keys to the scroll
// offset and returns true when the key was consumed (so the Update
// caller knows to skip the dismiss path). Recognised keys:
//
//   - j / down: line down
//   - k / up:   line up
//   - pgdown / space: half-page down
//   - pgup:     half-page up
//   - ctrl+d / ctrl+u: half-page down / up (canonical vim)
//   - ctrl+f / ctrl+b: full-page down / up (canonical vim)
//   - g / home: jump to top
//   - G / end:  jump to bottom
//
// Half / full-page steps come from the cursor package so the help
// overlay scrolls with the same cadence as the alerts and silences
// pages. The offset is clamped to [0, lastMaxScroll] inline so a
// held-down key doesn't strand the offset past the last row before
// View has a chance to re-clamp.
func (h *Help) scrollByKey(key string) bool {
	half := cursor.HalfPageStep(h.lastBodyHeight)
	full := cursor.FullPageStep(h.lastBodyHeight)
	switch key {
	case "j", "down":
		h.scroll++
	case "k", "up":
		h.scroll--
	case "pgdown", "space", "ctrl+d":
		h.scroll += half
	case "pgup", "ctrl+u":
		h.scroll -= half
	case "ctrl+f":
		h.scroll += full
	case "ctrl+b":
		h.scroll -= full
	case "g", "home":
		h.scroll = 0
	case "G", "end":
		h.scroll = h.lastMaxScroll
	default:
		return false
	}
	h.clampScroll()
	return true
}

// clampScroll keeps the scroll offset inside [0, lastMaxScroll].
// The View-side clamp still runs on every render — this one mirrors
// the math at Update-time so the in-flight offset stays sane
// between renders (e.g. when several scroll keys fire before View
// runs again).
func (h *Help) clampScroll() {
	if h.scroll > h.lastMaxScroll {
		h.scroll = h.lastMaxScroll
	}
	if h.scroll < 0 {
		h.scroll = 0
	}
}

// View renders the four columns into the rectangle the app shell
// hands over. The outer frame is drawn by the app, not by this view.
func (h *Help) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}

	cols := h.columns()
	colWidth := max(width/len(cols), 12)

	// Clamp scroll to the rendered content. Done at View-time
	// rather than on each wheel tick so a window resize that
	// shrinks/expands the visible area doesn't leave the offset
	// pointing past the last row; the next render heals it. Cache
	// height / maxScroll so the scroll-key handler in Update can
	// step by half / full page and clamp without re-measuring
	// columns.
	maxScroll := maxScrollOffset(cols, height)
	h.lastBodyHeight = height
	h.lastMaxScroll = maxScroll
	if h.scroll > maxScroll {
		h.scroll = maxScroll
	}
	if h.scroll < 0 {
		h.scroll = 0
	}

	rows := h.composeRows(cols, colWidth, height, h.scroll)
	return strings.Join(rows, "\n")
}

// maxScrollOffset is the largest scroll value that still leaves
// at least one row visible. Negative results clamp to zero (every
// column fits inside height — nothing to scroll).
func maxScrollOffset(cols [][]string, height int) int {
	tallest := 0
	for _, c := range cols {
		if len(c) > tallest {
			tallest = len(c)
		}
	}
	overflow := tallest - height
	if overflow < 0 {
		return 0
	}
	return overflow
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
// the App panel takes care of side borders. scroll shifts the
// starting row so a help payload that overflows the overlay's
// height can be walked downward by mouse-wheel ticks.
func (h *Help) composeRows(cols [][]string, colWidth, height, scroll int) []string {
	maxLen := 0
	for _, c := range cols {
		if len(c) > maxLen {
			maxLen = len(c)
		}
	}
	end := min(scroll+height, maxLen)

	rows := make([]string, 0, end-scroll)
	for r := scroll; r < end; r++ {
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

// ligatureProneKeys is the set of single-character key labels
// that combine with `<` or `>` into a programming-font ligature
// (`<-`, `->`, `<=`, `=>`, `<>`). On terminals with
// programming-ligature fonts (ghostty, kitty, wezterm with
// JetBrains Mono / Fira Code / …) the binding `-` renders as
// `<->` and ligatures into a fancy double-edged arrow that hides
// the actual key. Bindings using these keys are rendered with
// square brackets (`[-]`) instead of the angle-bracket chip;
// `[ ]` does not form a ligature in any common programming font.
// Every other key keeps its `<key>` rendering so existing test
// assertions on `<0>`, `<Enter>`, etc. stay literal.
var ligatureProneKeys = map[string]struct{}{
	"-": {},
	"=": {},
	"<": {},
	">": {},
}

// ChipText returns the bracketed form of key, swapping to square
// brackets for ligature-prone single-character keys so programming-
// ligature fonts don't mangle them. Exported because the footer hint
// bar and the top panel's action chips render the same shape; a
// single rule keeps every binding chrome consistent on a future
// ligature addition.
func ChipText(key string) string {
	if _, prone := ligatureProneKeys[key]; prone {
		return "[" + key + "]"
	}
	return "<" + key + ">"
}

// entry renders one binding as "<key>  description" (or "[-]
// reset" for ligature-prone keys) with the key chip styled as a
// hint helper key (theme.Hint.HelpKey).
func (h *Help) entry(key, desc string) string {
	chip := h.opts.Styles.Hint.HelpKey.Render(ChipText(key))
	return chip + "  " + desc
}

// headingLabel renders a column heading in the table-header colour
// (uppercase to match the list-page table headers).
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
		return format.SGRTruncate(s, w)
	}
	return s + strings.Repeat(" ", w-cur)
}

// itoa is a small allocation-free int → string for the numeric
// tenant quick-switch (1-9 only).
func itoa(n int) string {
	if n < 0 || n > 9 {
		return ""
	}
	return string(rune('0' + n))
}

// SPDX-License-Identifier: Apache-2.0

// Package help renders the k9s-style help overlay opened by `?`.
// The view is a bordered box titled "Help" hosting four columns:
//
//		RESOURCE  |  GENERAL  |  NAVIGATION  |  COMMANDS
//
//	  - RESOURCE lists the active page's view-specific verbs (drill,
//	    silence, the `Shift+`-letter sort shortcuts, …) plus the
//	    global numeric tenant quick-switch (`<0>` all, `<1>`-`<9>` per
//	    configured backend) — the one column that actually depends on
//	    what the user is looking at. Sorts live here, not in a
//	    separate column, matching k9s where every view binding
//	    (`Sort Age`, `Sort Name`, …) sits under RESOURCE.
//	  - GENERAL is the curated globals catalogue (`:cmd` `/` `?`
//	    `Esc` `q` `Ctrl+C` `Ctrl+T` `r`) plus the active page's
//	    cross-cutting Shared verbs (`Space` mark), mirroring k9s
//	    where mark lives under GENERAL on every table view.
//	  - NAVIGATION is the table-context vim motions only (`j` `k`
//	    `h` `l` `gg` `G` `Ctrl+D` `Ctrl+U` `Ctrl+F` `Ctrl+B`) — pure
//	    cursor movement, no verbs, per the k9s column split.
//	  - COMMANDS lists the `:`-bar built-in aliases folded by synonym
//	    (`silences, sil`) plus a `USER` sub-section showing
//	    `short → expanded` rows when the operator has registered any
//	    custom aliases (ADR 0038).
//
// Chips render bold in the key colour, with the numeric tenant
// quick-switch keys (`<0>`-`<9>`) in the distinct num-key colour —
// k9s's menu styling (`KeyColor` vs `NumKeyColor`, both bold).
//
// Read-only mode hides every Dangerous binding from RESOURCE.
package help

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
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

	// PageBindings is the active page's Bindings() output. Rendered
	// in RESOURCE (after the ReadOnly filter) — including the
	// `Shift+`-letter sort shortcuts, which k9s keeps under RESOURCE
	// rather than a separate column. Two exceptions are reorganised
	// by columns(): Shared verbs (`Space` mark) fold into GENERAL,
	// and verbs whose key a global or table motion already owns are
	// dropped so each chip renders under exactly one heading.
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

	// Commands is the resolver's built-in alias catalogue (ADR
	// 0038): one AliasGroup per registration, canonical name
	// first. The COMMANDS column folds each group onto a single
	// row (`silences, sil`) so the catalogue reads as a flat
	// resource list. Empty / nil renders just the heading so the
	// 4-column layout stays uniform across boot states.
	Commands []cmdbar.AliasGroup

	// UserCommands is the user-registered alias catalogue. Rendered
	// under a `USER` sub-heading in the COMMANDS column as
	// `short → expanded` rows. Nil / empty drops the sub-heading
	// entirely so the column doesn't grow a hollow section.
	UserCommands []cmdbar.UserAlias

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

// colGap is the inter-column spacing the row joiner reserves so a
// cell whose content fills the column doesn't touch the next
// column's chip. Two spaces matches the panel-side colGap so chrome
// gap math stays uniform across the TUI.
const colGap = 2

// columns builds the four column bodies (heading + entry list each)
// in display order. Each entry is already styled so the row joiner
// can pad without re-measuring.
//
// The k9s rule is that every binding lives under exactly one heading.
// Pages re-advertise cross-cutting bindings on their hint strip (the
// global `/` filter and `r` refresh, the table-wide `Space` mark), so
// the overlay reorganises the page's verbs before rendering: Shared
// verbs (mark) fold into GENERAL; verbs whose key a global or a table
// motion already owns are dropped from RESOURCE; the rest — sorts
// included — stay in RESOURCE.
func (h *Help) columns() [][]string {
	verbs := filterDangerous(h.opts.PageBindings, h.opts.ReadOnly)
	shared, verbs := partitionShared(verbs)
	verbs = dropReserved(verbs, h.opts.Globals, h.opts.TableMotions)
	return [][]string{
		h.resourceColumn(verbs),
		h.staticColumn("GENERAL", mergeGeneral(h.opts.Globals, shared)),
		h.staticColumn("NAVIGATION", h.opts.TableMotions),
		h.commandsColumn(),
	}
}

// partitionShared splits page verbs into the k9s "shared" set (the
// cross-cutting `Space`/mark every list page reuses) and the
// view-specific remainder. Shared verbs render in GENERAL alongside
// the dispatcher globals — mirroring k9s, where mark lives under
// GENERAL on every table view — while the remainder stays in RESOURCE.
func partitionShared(verbs []action.Action) (shared, rest []action.Action) {
	for _, a := range verbs {
		if a.Shared {
			shared = append(shared, a)
			continue
		}
		rest = append(rest, a)
	}
	return shared, rest
}

// dropReserved removes any RESOURCE verb whose key already appears in
// the GENERAL (globals) or NAVIGATION (table motions) column. A page
// surfaces `/` filter and `r` refresh on its hint strip for
// discoverability even though both are globals; the overlay drops the
// RESOURCE copy so each chip renders once, under the cross-cutting
// heading that owns it.
func dropReserved(verbs, globals, motions []action.Action) []action.Action {
	reserved := make(map[string]struct{}, len(globals)+len(motions))
	for _, a := range globals {
		reserved[a.Key] = struct{}{}
	}
	for _, a := range motions {
		reserved[a.Key] = struct{}{}
	}
	out := make([]action.Action, 0, len(verbs))
	for _, a := range verbs {
		if _, dup := reserved[a.Key]; dup {
			continue
		}
		out = append(out, a)
	}
	return out
}

// mergeGeneral appends the shared page verbs to the dispatcher globals
// for the GENERAL column, skipping any whose key a global already
// advertises so the column never doubles a chip.
func mergeGeneral(globals, shared []action.Action) []action.Action {
	seen := make(map[string]struct{}, len(globals))
	for _, a := range globals {
		seen[a.Key] = struct{}{}
	}
	out := make([]action.Action, 0, len(globals)+len(shared))
	out = append(out, globals...)
	for _, a := range shared {
		if _, dup := seen[a.Key]; dup {
			continue
		}
		seen[a.Key] = struct{}{}
		out = append(out, a)
	}
	return out
}

// synonymJoiner separates the canonical and short names on a
// folded COMMANDS row (`silences, sil`). userAliasArrow is the
// arrow between a user alias and its expansion. Lifted out of the
// render path so the ADR 0038 formatting contract has a single
// source.
const (
	synonymJoiner  = ", "
	userAliasArrow = " → "
)

// commandsColumn renders the ADR 0038 catalogue. The heading stays
// in place even when every catalogue is empty so the 4-column
// layout is stable across boot states (no commands wired yet) and
// shell states (no user aliases configured).
func (h *Help) commandsColumn() []string {
	rowCount := 1 + len(h.opts.Commands)
	if n := len(h.opts.UserCommands); n > 0 {
		rowCount += 2 + n // blank gap + USER subheading + n rows
	}
	out := make([]string, 0, rowCount)
	out = append(out, h.headingLabel("COMMANDS"))
	for _, g := range h.opts.Commands {
		out = append(out, h.opts.Styles.Hint.DefaultFg.Render(strings.Join(g.Names, synonymJoiner)))
	}
	if len(h.opts.UserCommands) > 0 {
		// Visual gap before the USER subheading so a reader's eye
		// stops at the section break rather than scanning past it.
		// The subheading is intentionally rendered weaker than a
		// column heading so it reads as a nested section, not a
		// sixth column.
		out = append(out, "", h.subheadingLabel("USER"))
		for _, a := range h.opts.UserCommands {
			out = append(out, h.opts.Styles.Hint.DefaultFg.Render(a.Short+userAliasArrow+a.Expanded))
		}
	}
	return out
}

// resourceColumn lists the tenant numeric quick-switch (`<0>` all,
// `<1>` … `<9>` per configured backend) followed by the active
// page's non-sort verbs. Empty backends just drop the numeric block.
// Chip alignment is handled inside alignedColumn.
func (h *Help) resourceColumn(verbs []action.Action) []string {
	parts := make([]rowParts, 0, 1+len(h.opts.Tenants)+len(verbs))
	if len(h.opts.Tenants) > 0 {
		parts = append(parts, rowParts{key: "0", desc: "all"})
		for i, name := range h.opts.Tenants {
			if i >= 9 {
				break
			}
			parts = append(parts, rowParts{key: itoa(i + 1), desc: name})
		}
	}
	for _, a := range verbs {
		parts = append(parts, rowParts{key: a.ChipKey(), desc: a.Description})
	}
	return h.alignedColumn("RESOURCE", parts)
}

// staticColumn renders a heading + a list of pre-curated actions
// with chips padded to the column's widest entry so descriptions
// line up. Globals and table motions both flow through here.
func (h *Help) staticColumn(name string, entries []action.Action) []string {
	filtered := filterDangerous(entries, h.opts.ReadOnly)
	parts := make([]rowParts, len(filtered))
	for i, a := range filtered {
		parts[i] = rowParts{key: a.ChipKey(), desc: a.Description}
	}
	return h.alignedColumn(name, parts)
}

// composeRows zips columns into rows. Each cell renders into the
// leftmost colWidth-colGap cells of its colWidth slot — the trailing
// colGap stays blank so neighbouring columns never touch even when
// a truncated description filled the visible budget. Each row is
// exactly colWidth*len(cols) columns wide; the App panel takes care
// of side borders. scroll shifts the starting row so a help payload
// that overflows the overlay's height can be walked downward by
// mouse-wheel ticks.
func (h *Help) composeRows(cols [][]string, colWidth, height, scroll int) []string {
	maxLen := 0
	for _, c := range cols {
		if len(c) > maxLen {
			maxLen = len(c)
		}
	}
	end := min(scroll+height, maxLen)

	contentWidth := max(colWidth-colGap, 1)
	gap := strings.Repeat(" ", colWidth-contentWidth)
	rows := make([]string, 0, end-scroll)
	for r := scroll; r < end; r++ {
		var b strings.Builder
		for i, col := range cols {
			cell := ""
			if r < len(col) {
				cell = col[r]
			}
			if i == len(cols)-1 {
				// Last column carries no trailing gap — nothing to
				// separate from.
				b.WriteString(padRight(cell, colWidth))
				continue
			}
			b.WriteString(padRight(cell, contentWidth))
			b.WriteString(gap)
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
var ligatureProneKeys = map[string]struct{}{
	"-": {},
	"=": {},
	"<": {},
	">": {},
}

// ChipText is shared by the footer hint bar and the top-panel hint
// grid so chip shape stays uniform across chrome. Square brackets
// replace angle brackets for ligature-prone keys to prevent
// programming-font collisions (see ligatureProneKeys). Bare uppercase
// single letters expand to shift-letter so `s` and `S` stay visually
// distinct when a view binds both (see ADR 0037).
func ChipText(key string) string {
	if _, prone := ligatureProneKeys[key]; prone {
		return "[" + key + "]"
	}
	if len(key) == 1 && key[0] >= 'A' && key[0] <= 'Z' {
		key = "Shift+" + string(key[0]-'A'+'a')
	}
	return "<" + strings.ReplaceAll(strings.ToLower(key), "+", "-") + ">"
}

// rowParts is the (chip-key, description) pair feeding
// alignedColumn. Bundled so the per-column widest-chip pass walks
// one slice instead of two.
type rowParts struct {
	key  string
	desc string
}

// alignedColumn renders a heading + every entry row, with chips
// padded to the column's widest visible chip so descriptions line
// up under one invisible left edge — the k9s layout rule. A single
// gap of two cells separates chip from description; the column
// joiner reserves another colGap cells of right-edge whitespace so
// neighbouring columns never touch.
func (h *Help) alignedColumn(heading string, parts []rowParts) []string {
	widest := 0
	chips := make([]string, len(parts))
	chipWidths := make([]int, len(parts))
	for i, p := range parts {
		chip := h.chipStyle(p.key).Render(ChipText(p.key))
		w := lipgloss.Width(chip)
		chips[i] = chip
		chipWidths[i] = w
		if w > widest {
			widest = w
		}
	}
	out := make([]string, 0, len(parts)+1)
	out = append(out, h.headingLabel(heading))
	for i, p := range parts {
		pad := strings.Repeat(" ", widest-chipWidths[i])
		out = append(out, chips[i]+pad+"  "+p.desc)
	}
	return out
}

// chipStyle picks a chip's colour the way k9s colours its menu: the
// numeric tenant quick-switch keys (`0`-`9`) render in the dedicated
// num-key colour (Hint.HelpKeyBold), every other key in the standard
// key colour (Hint.KeyBold). Both are bold — k9s draws every menu key
// with its `:b` attribute.
func (h *Help) chipStyle(key string) lipgloss.Style {
	if isNumericKey(key) {
		return h.opts.Styles.Hint.HelpKeyBold
	}
	return h.opts.Styles.Hint.KeyBold
}

// isNumericKey reports whether key is a single-digit tenant
// quick-switch mnemonic — the keys k9s renders in NumKeyColor (its
// predicate is `strconv.Atoi` succeeding on the menu mnemonic). a10r
// only ever binds the bare digits 0-9 in that slot, so a single-rune
// digit check matches without pulling in multi-digit edge cases.
func isNumericKey(key string) bool {
	return len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

// headingLabel renders a column heading in the table-header colour
// (uppercase to match the list-page table headers).
func (h *Help) headingLabel(name string) string {
	st := lipgloss.NewStyle().
		Foreground(h.opts.Styles.Table.Header.GetForeground()).
		Bold(true)
	return st.Render(name)
}

// subheadingLabel renders an in-column sub-section divider (today
// only the USER block inside COMMANDS). Same colour as a column
// heading so the visual family is consistent, but unbold so a
// reader's eye registers it as nested rather than as a peer column.
func (h *Help) subheadingLabel(name string) string {
	st := lipgloss.NewStyle().
		Foreground(h.opts.Styles.Table.Header.GetForeground())
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

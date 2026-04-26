// SPDX-License-Identifier: Apache-2.0

// Package tenant renders the tenant table per C3: one row per
// configured backend, with active-selection markers, connection
// state, and counts. The page emits SelectedMsg on submit; the
// wiring layer translates that into "update the active tenant
// set, pop back to the invoking page, kick the pollers."
package tenant

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Row is one tenant's renderable state. The wiring layer rebuilds
// the slice on every redraw from the configured backends + the
// poll-aggregated connection / count map.
type Row struct {
	Name    string
	Conn    header.ConnState
	Alerts  int
	Silence int
}

// SelectedMsg is emitted when the user accepts a selection. Empty
// Selections means "all tenants" (the C3 "0" quick-switch).
type SelectedMsg struct {
	Selections []string
}

// Page is the tenant table view.
type Page struct {
	styles theme.Styles

	rows   []Row
	cursor int
	topRow int                 // first visible row; reconciled in View
	marks  map[string]struct{} // selected tenant names

	// scope tracks the active tenant scope as observed from
	// app.ScopeChangedMsg — "all" includes every row; a single
	// name flags exactly that row as in-scope; comma-joined names
	// flag each one. The page does NOT mutate the scope itself
	// (the global numeric quick-switch owns that); it only mirrors
	// what the App announced so the user can see at a glance which
	// row is currently fanned-out.
	scope string
}

// New constructs an empty tenant page. The wiring layer feeds rows
// via SetRows on every redraw.
func New(styles theme.Styles) *Page {
	return &Page{styles: styles, marks: map[string]struct{}{}, scope: "all"}
}

// SetRows replaces the rendered rows. Used by the wiring layer
// instead of a poll.DataMsg path because tenant rows are derived
// from configuration + every (backend, resource) poller — there's
// no single DataMsg shape that fits.
func (p *Page) SetRows(rows []Row) {
	p.rows = rows
	if p.cursor >= len(rows) {
		p.cursor = max(len(rows)-1, 0)
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "tenant" }

// Title implements app.Page. Mirrors the rest of the list pages:
// `tenants(<scope>)[<count>]`. Scope is the active selection so
// the title carries the same scope label the user sees in the top
// panel.
func (p *Page) Title() string {
	scope := p.scope
	if scope == "" {
		scope = "all"
	}
	return fmt.Sprintf("tenants(%s)[%d]", scope, len(p.rows))
}

// HeaderContent implements app.Page. The backend count is in
// Title's `[N]` suffix; only the live mark count is interesting
// here, and only when at least one row is marked. Empty marks
// fold into the cursor-row submit, so an empty subtitle reads as
// "nothing pending."
func (p *Page) HeaderContent() string {
	if len(p.marks) == 0 {
		return ""
	}
	return fmt.Sprintf("%d selected", len(p.marks))
}

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "select", View: "tenant"},
		{Key: "Space", Description: "toggle", View: "tenant"},
		{Key: "a", Description: "all", View: "tenant"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case app.GoToFirstRowMsg:
		p.cursor = 0
		return p, nil
	case app.ScopeChangedMsg:
		// The App's LayerGlobal numeric quick-switch (`<0>` all,
		// `<1>`-`<9>` per backend) emits this. Mirroring it here
		// lets the table show the user which row is fanned-out
		// without forcing them to glance at the top panel.
		p.scope = m.Scope
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch keyMsg.String() {
	case "j", "down":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "g":
		p.cursor = 0
	case "G":
		p.cursor = max(len(p.rows)-1, 0)
	case "space":
		p.toggleAtCursor()
	case "a":
		p.selectAll()
	case "enter":
		cmd := p.submit()
		return p, cmd
	}
	// Numeric quick-switch (`0`, `1`-`9`) is owned by the App's
	// LayerGlobal binding (see app.registerTenantBindings) — it
	// emits ScopeChangedMsg so every page reacts the same way.
	// The tenant page therefore does NOT bind the digits locally;
	// the dispatcher consumes them before forwardToTop runs.
	return p, nil
}

func (p *Page) toggleAtCursor() {
	if p.cursor >= len(p.rows) {
		return
	}
	name := p.rows[p.cursor].Name
	if _, ok := p.marks[name]; ok {
		delete(p.marks, name)
		return
	}
	p.marks[name] = struct{}{}
}

func (p *Page) selectAll() {
	for _, r := range p.rows {
		p.marks[r.Name] = struct{}{}
	}
}

// submit returns a Cmd emitting SelectedMsg with whatever's
// currently marked. Empty marks fall back to "the cursor row" so
// Enter without prior Space behaves intuitively — single-select on
// today's UX, multi-select on Space-then-Enter.
func (p *Page) submit() tea.Cmd {
	if len(p.marks) > 0 {
		sel := make([]string, 0, len(p.marks))
		for _, r := range p.rows {
			if _, ok := p.marks[r.Name]; ok {
				sel = append(sel, r.Name)
			}
		}
		return func() tea.Msg { return SelectedMsg{Selections: sel} }
	}
	if p.cursor < len(p.rows) {
		single := []string{p.rows[p.cursor].Name}
		return func() tea.Msg { return SelectedMsg{Selections: single} }
	}
	return nil
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.rows) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render("no backends configured")
	}
	maxRows := min(height, len(p.rows))
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.rows))
	out := make([]string, 0, end-p.topRow)
	rows := p.rowsSorted()
	for i := p.topRow; i < end; i++ {
		row := rows[i]
		mark := "[ ]"
		if _, ok := p.marks[row.Name]; ok {
			mark = "[x]"
		}
		// Glyph indicates whether the row is part of the active
		// global scope (the numeric quick-switch state). `●`
		// reads at a glance against the row body and stays legible
		// alongside the [ ]/[x] mark column.
		scopeGlyph := " "
		if p.scopeIncludes(row.Name) {
			scopeGlyph = "●"
		}
		body := fmt.Sprintf("%s %s %s %s  alerts:%d  silences:%d",
			mark, scopeGlyph, row.Conn.String(), row.Name, row.Alerts, row.Silence)
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		// Pad to width before applying the cursor style so the
		// background extends across the whole row k9s-style.
		line := padRight(prefix+body, width)
		switch {
		case i == p.cursor:
			line = p.styles.Table.Cursor.Render(line)
		case p.scopeIncludes(row.Name):
			// In-scope rows tint foreground only — same affordance
			// as marked rows on the alerts page so the two list-
			// page conventions agree.
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		}
		out = append(out, line)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

// scopeIncludes reports whether the named tenant is part of the
// active global scope. "all" / empty includes everyone; otherwise
// the scope is matched against the comma-joined name list (so the
// future Ctrl+T multi-select path "prod,staging" lights up both
// rows).
func (p *Page) scopeIncludes(name string) bool {
	scope := strings.TrimSpace(p.scope)
	if scope == "" || scope == "all" {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == name {
			return true
		}
	}
	return false
}

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
func (p *Page) reconcileScroll(maxRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	maxTop := max(len(p.rows)-maxRows, 0)
	if p.topRow > maxTop {
		p.topRow = maxTop
	}
	if p.topRow < 0 {
		p.topRow = 0
	}
}

// padRight pads s with trailing spaces to w columns so the
// cursor's background extends across the whole row.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// rowsSorted returns the rows alphabetically by Name so the
// numeric quick-switch is stable across redraws.
func (p *Page) rowsSorted() []Row {
	out := make([]Row, len(p.rows))
	copy(out, p.rows)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

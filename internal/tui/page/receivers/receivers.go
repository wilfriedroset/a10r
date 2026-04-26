// SPDX-License-Identifier: Apache-2.0

// Package receivers renders the receivers list. v0.1 ships a
// trivial single-column table — receivers carry only a Name on
// the AM side, so the page's value is mostly the drill-down: an
// Enter on a row pushes the alerts page filtered by `receiver=…`.
package receivers

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// DrillRequestMsg is emitted on Enter. The wiring layer (cmd/tui.go)
// catches it and pushes an alerts page pre-filtered by receiver.
// Decoupled this way so the receivers page does NOT import the
// alerts page (would be a maintenance edge — both pages can land
// in any order).
type DrillRequestMsg struct {
	Receiver string
}

// Page is the receivers list view.
//
// Receivers are flat strings on the AM side; the only multi-
// backend shaping the page does is union snapshots so the user
// can quickly see which receivers exist across the active scope.
// The drill-down keeps the behaviour the user expects (Enter on
// a row → DrillRequestMsg with the receiver name) since the
// receiver name is unique per backend in practice.
type Page struct {
	styles theme.Styles

	// byTenant holds the most recent snapshot per backend, keyed
	// by the poll.DataMsg.Tenant tag.
	byTenant map[string][]string
	view     []string // filtered + scoped + de-duplicated subset
	cursor   int
	topRow   int // first visible row; reconciled against cursor on every render

	// filter is the active substring filter; preFilter is the
	// snapshot the page restores on PromptCancelledMsg per the
	// shared `/`-prompt contract (see alerts page for the full
	// lifecycle doc).
	filter    string
	preFilter *string

	// scope mirrors the active tenant scope ("all" / single name
	// / comma-joined subset). Updated by app.ScopeChangedMsg.
	scope string
}

// scopeAll is the canonical "every configured tenant" label.
const scopeAll = "all"

// New constructs an empty receivers page.
func New(styles theme.Styles) *Page {
	return &Page{
		styles:   styles,
		byTenant: map[string][]string{},
		scope:    scopeAll,
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "receivers" }

// Title implements app.Page. Mirrors the alerts shape:
// `receivers(<scope>)[<count>]` or `receivers(<scope>)[F/T]`
// while a filter is active.
func (p *Page) Title() string {
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	total := len(p.unionScoped())
	if p.filter != "" {
		return fmt.Sprintf("receivers(%s)[%d/%d]", scope, len(p.view), total)
	}
	return fmt.Sprintf("receivers(%s)[%d]", scope, total)
}

// scopeIncludes reports whether tenant should appear in the view.
func (p *Page) scopeIncludes(tenant string) bool {
	scope := strings.TrimSpace(p.scope)
	if scope == "" || scope == scopeAll {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == tenant {
			return true
		}
	}
	return false
}

// unionScoped returns the de-duplicated set of receiver names
// across every in-scope tenant, sorted alphabetically. Used by
// Title and recompute.
func (p *Page) unionScoped() []string {
	seen := map[string]struct{}{}
	for tenant, names := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, n := range names {
			seen[n] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HeaderContent implements app.Page. Surfaces the active filter
// (when any) so the user can see what's been applied without
// re-opening the prompt. Empty otherwise — count lives in Title.
func (p *Page) HeaderContent() string {
	if p.filter != "" {
		return "filter:" + p.filter
	}
	return ""
}

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "drill", View: "receivers"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.DataMsg:
		recs, ok := m.Resource.([]backend.Receiver)
		if !ok {
			return p, nil
		}
		names := make([]string, len(recs))
		for i, r := range recs {
			names[i] = r.Name
		}
		sort.Strings(names)
		p.byTenant[m.Tenant] = names
		p.recompute()
		return p, nil
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return p, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		return p, nil
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.handleFilterPrompt(m)
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleFilterPrompt mirrors the alerts page's lifecycle handler.
// See internal/tui/page/alerts/alerts.go for the full doc.
func (p *Page) handleFilterPrompt(msg tea.Msg) {
	switch m := msg.(type) {
	case footer.PromptOpenedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		snap := p.filter
		p.preFilter = &snap
		if p.filter != "" {
			p.filter = ""
			p.recompute()
		}
	case footer.PromptChangedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		p.filter = m.Value
		p.recompute()
	case footer.PromptSubmittedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		p.filter = m.Value
		p.preFilter = nil
		p.recompute()
	case footer.PromptCancelledMsg:
		if m.Mode != footer.PromptFilter || p.preFilter == nil {
			return
		}
		p.filter = *p.preFilter
		p.preFilter = nil
		p.recompute()
	}
}

// recompute rebuilds the filtered view from byTenant + p.scope +
// p.filter and clamps the cursor to the new range.
func (p *Page) recompute() {
	scoped := p.unionScoped()
	if p.filter == "" {
		p.view = scoped
	} else {
		q := strings.ToLower(p.filter)
		p.view = p.view[:0]
		for _, name := range scoped {
			if strings.Contains(strings.ToLower(name), q) {
				p.view = append(p.view, name)
			}
		}
	}
	if p.cursor >= len(p.view) {
		p.cursor = max(len(p.view)-1, 0)
	}
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "j", "down":
		if p.cursor < len(p.view)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "G":
		p.cursor = max(len(p.view)-1, 0)
	case "g":
		p.cursor = 0
	case "enter":
		if p.cursor < len(p.view) {
			rec := p.view[p.cursor]
			return p, func() tea.Msg { return DrillRequestMsg{Receiver: rec} }
		}
	}
	return p, nil
}

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
func (p *Page) reconcileScroll(maxRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	maxTop := max(len(p.view)-maxRows, 0)
	if p.topRow > maxTop {
		p.topRow = maxTop
	}
	if p.topRow < 0 {
		p.topRow = 0
	}
}

// padRight pads s with trailing spaces so it occupies exactly w
// columns. Truncates when s already exceeds w. Used to size the
// cursor row to the full body width before the style wraps it,
// so the cursor's background extends across the row.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.view) == 0 {
		msg := "no receivers (yet)"
		if len(p.unionScoped()) > 0 && p.filter != "" {
			msg = "no receivers match the active filter"
		}
		return p.styles.Body.Default.Width(width).Height(height).Render(msg)
	}
	maxRows := min(height, len(p.view))
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.view))
	rows := make([]string, 0, end-p.topRow)
	for i := p.topRow; i < end; i++ {
		text := p.view[i]
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		// Pad to width before applying the cursor style so the
		// background extends across the whole row k9s-style.
		row := padRight(prefix+text, width)
		if i == p.cursor {
			row = p.styles.Table.Cursor.Render(row)
		}
		rows = append(rows, row)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

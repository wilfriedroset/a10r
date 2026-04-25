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
type Page struct {
	styles theme.Styles

	all    []string
	cursor int
}

// New constructs an empty receivers page.
func New(styles theme.Styles) *Page {
	return &Page{styles: styles}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "receivers" }

// HeaderContent implements app.Page.
func (p *Page) HeaderContent() string {
	return fmt.Sprintf("%d receivers", len(p.all))
}

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "drill", View: "receivers"},
		{Key: "?", Description: "help", View: ""},
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
		p.all = make([]string, len(recs))
		for i, r := range recs {
			p.all[i] = r.Name
		}
		sort.Strings(p.all)
		if p.cursor >= len(p.all) {
			p.cursor = max(len(p.all)-1, 0)
		}
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "j", "down":
		if p.cursor < len(p.all)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "G":
		p.cursor = max(len(p.all)-1, 0)
	case "g":
		p.cursor = 0
	case "enter":
		if p.cursor < len(p.all) {
			rec := p.all[p.cursor]
			return p, func() tea.Msg { return DrillRequestMsg{Receiver: rec} }
		}
	}
	return p, nil
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.all) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render("no receivers (yet)")
	}
	maxRows := min(height, len(p.all))
	rows := make([]string, 0, maxRows)
	for i := range maxRows {
		row := p.all[i]
		if i == p.cursor {
			row = "▸ " + row
		} else {
			row = "  " + row
		}
		rows = append(rows, row)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

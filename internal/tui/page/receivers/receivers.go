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
	topRow int // first visible row; reconciled against cursor on every render
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

// Title implements app.Page.
func (p *Page) Title() string { return fmt.Sprintf("receivers[%d]", len(p.all)) }

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
	case app.GoToFirstRowMsg:
		p.cursor = 0
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

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
func (p *Page) reconcileScroll(maxRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	maxTop := max(len(p.all)-maxRows, 0)
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
	if len(p.all) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render("no receivers (yet)")
	}
	maxRows := min(height, len(p.all))
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.all))
	rows := make([]string, 0, end-p.topRow)
	for i := p.topRow; i < end; i++ {
		text := p.all[i]
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

// SPDX-License-Identifier: Apache-2.0

// Package silences renders the silences list page. The page
// surfaces the Silence write actions (new, edit, expire, editor)
// behind Dangerous bindings so read-only mode hides them all.
//
// Silence form (#30), editor handoff (#31), and the actual write
// API calls land in their own commits; v0.1 of this page wires
// the bindings to placeholder flashes so the affordances are
// discoverable in the meantime.
package silences

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// SortKey enumerates the sortable columns for the silences table.
type SortKey int

const (
	// SortByEndsAt is the default — silences expiring soonest at
	// the top (E2).
	SortByEndsAt SortKey = iota
	// SortByStartsAt sorts by start time.
	SortByStartsAt
	// SortByCreatedBy sorts alphabetically by creator.
	SortByCreatedBy
	// SortByState sorts by silence state (active, pending, expired).
	SortByState
)

// String returns the column-header label.
func (s SortKey) String() string {
	switch s {
	case SortByEndsAt:
		return "ends"
	case SortByStartsAt:
		return "starts"
	case SortByCreatedBy:
		return "by"
	case SortByState:
		return "state"
	}
	return "?"
}

// Page is the silences list view.
type Page struct {
	styles theme.Styles
	now    func() time.Time

	all    []backend.Silence
	view   []backend.Silence
	cursor int

	sort    SortKey
	sortAsc bool
	focusID string
}

// New constructs an empty silences page.
func New(styles theme.Styles, now func() time.Time) *Page {
	if now == nil {
		now = time.Now
	}
	return &Page{
		styles:  styles,
		now:     now,
		sort:    SortByEndsAt,
		sortAsc: true, // soonest-expiring first
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "silences" }

// HeaderContent implements app.Page.
func (p *Page) HeaderContent() string {
	dir := "↓"
	if p.sortAsc {
		dir = "↑"
	}
	return fmt.Sprintf("sort:%s %s · %d silences", p.sort, dir, len(p.view))
}

// Bindings implements app.Page. Every write action carries
// Dangerous so read-only mode (C4) hides them via the action
// registry.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "n", Description: "new", View: "silences", Dangerous: true},
		{Key: "e", Description: "edit", View: "silences", Dangerous: true},
		{Key: "x", Description: "expire", View: "silences", Dangerous: true},
		{Key: "Ctrl+E", Description: "editor", View: "silences", Dangerous: true},
		{Key: "Ctrl+X", Description: "bulk expire", View: "silences", Dangerous: true, Bulk: true},
		{Key: "?", Description: "help", View: ""},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.DataMsg:
		s, ok := m.Resource.([]backend.Silence)
		if !ok {
			return p, nil
		}
		p.all = s
		p.recompute()
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleMotion(m) {
		return p, nil
	}
	if p.handleSort(m) {
		return p, nil
	}
	return p.handleAction(m)
}

func (p *Page) handleMotion(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "j", "down":
		if p.cursor < len(p.view)-1 {
			p.cursor++
			p.snapshotFocus()
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
			p.snapshotFocus()
		}
	case "G":
		p.cursor = max(len(p.view)-1, 0)
		p.snapshotFocus()
	case "ctrl+d":
		p.cursor = min(p.cursor+10, max(len(p.view)-1, 0))
		p.snapshotFocus()
	case "ctrl+u":
		p.cursor = max(p.cursor-10, 0)
		p.snapshotFocus()
	default:
		return false
	}
	return true
}

func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "shift+e", "E":
		p.sort = SortByEndsAt
	case "shift+s", "S":
		p.sort = SortByStartsAt
	case "shift+c", "C":
		p.sort = SortByCreatedBy
	case "shift+t", "T":
		p.sort = SortByState
	default:
		return false
	}
	p.recompute()
	return true
}

func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "n":
		return p, flashFn(footer.FlashWarn, "silence form arrives in #30")
	case "e":
		return p, flashFn(footer.FlashWarn, "silence edit arrives in #30")
	case "x":
		return p, flashFn(footer.FlashWarn, "silence expire arrives in #30 (with confirm)")
	case "ctrl+e":
		return p, flashFn(footer.FlashWarn, "$EDITOR handoff arrives in #31")
	case "ctrl+x":
		return p, flashFn(footer.FlashWarn, "bulk expire arrives in #30")
	}
	return p, nil
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.view) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render(p.emptyState())
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-2)
	footerLine := fmt.Sprintf("  %d silences · cursor=%d", len(p.view), p.cursor+1)
	body := strings.Join([]string{headerLine, rows, footerLine}, "\n")
	return lipgloss.NewStyle().Width(width).Render(body)
}

func (p *Page) emptyState() string {
	if len(p.all) == 0 {
		return "no silences (yet) — `n` creates one once #30 lands"
	}
	return "no silences in view"
}

func (p *Page) renderHeader(width int) string {
	titles := []SortKey{SortByEndsAt, SortByStartsAt, SortByCreatedBy, SortByState}
	parts := make([]string, len(titles))
	for i, k := range titles {
		label := k.String()
		if k == p.sort {
			arrow := "↓"
			if p.sortAsc {
				arrow = "↑"
			}
			label = label + " " + arrow
		}
		parts[i] = label
	}
	return p.padColumns(parts, width)
}

func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range p.view {
		if i >= maxRows {
			break
		}
		row := []string{
			header.FormatAge(p.now(), s.EndsAt),
			header.FormatAge(p.now(), s.StartsAt),
			s.CreatedBy,
			string(s.State),
		}
		line := p.padColumns(row, width)
		if i == p.cursor {
			line = "▸ " + line
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func (p *Page) padColumns(parts []string, width int) string {
	cols := []int{14, 14, 0, 12} // ends, starts, by (flex), state
	cols[2] = max(width-cols[0]-cols[1]-cols[3]-2, 10)
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padRight(v, cols[i]))
	}
	return b.String()
}

func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String()
}

func (p *Page) recompute() {
	p.view = make([]backend.Silence, len(p.all))
	copy(p.view, p.all)
	sortSilences(p.view, p.sort, p.sortAsc)
	if p.focusID != "" {
		for i, s := range p.view {
			if s.ID == p.focusID {
				p.cursor = i
				return
			}
		}
	}
	if p.cursor >= len(p.view) {
		p.cursor = max(len(p.view)-1, 0)
	}
	p.snapshotFocus()
}

func (p *Page) snapshotFocus() {
	if p.cursor < len(p.view) {
		p.focusID = p.view[p.cursor].ID
		return
	}
	p.focusID = ""
}

func sortSilences(out []backend.Silence, key SortKey, asc bool) {
	less := lessFor(key)
	sort.SliceStable(out, func(i, j int) bool {
		if asc {
			return less(out[i], out[j])
		}
		return less(out[j], out[i])
	})
}

func lessFor(key SortKey) func(a, b backend.Silence) bool {
	switch key {
	case SortByStartsAt:
		return func(a, b backend.Silence) bool { return a.StartsAt.Before(b.StartsAt) }
	case SortByCreatedBy:
		return func(a, b backend.Silence) bool { return a.CreatedBy < b.CreatedBy }
	case SortByState:
		return func(a, b backend.Silence) bool { return a.State < b.State }
	default: // SortByEndsAt
		return func(a, b backend.Silence) bool { return a.EndsAt.Before(b.EndsAt) }
	}
}

// flashFn returns a Cmd that emits a Warn flash. The placeholder
// actions on this page all use Warn (the affordances are wired
// but the actual write isn't yet) so the helper hard-codes the
// level — no caller wants anything else today.
func flashFn(level footer.FlashLevel, text string) tea.Cmd { //nolint:unparam // level kept for the eventual non-Warn callers in #30/#31
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

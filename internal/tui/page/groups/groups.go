// SPDX-License-Identifier: Apache-2.0

// Package groups renders the alert-groups view: a two-level tree
// where each group label-set expands to its member alerts. Enter
// on a group toggles expand/collapse; Enter on a leaf drills to
// the alert-detail page (DrillAlertMsg). `s` requests a silence
// over the group's common-labels intersection (DrillSilenceMsg).
package groups

import (
	"fmt"
	"maps"
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

// DrillAlertMsg is emitted on Enter against a leaf row. The wiring
// layer pushes the alert detail page.
type DrillAlertMsg struct {
	Alert backend.Alert
}

// DrillSilenceMsg is emitted on `s` against a group row. CommonLabels
// is the intersection of every alert's labels in that group — the
// silence form opens pre-populated with those matchers.
type DrillSilenceMsg struct {
	CommonLabels map[string]string
}

// Page is the groups view.
type Page struct {
	styles theme.Styles

	all      []backend.AlertGroup
	expanded []bool // per-group flag, indexed against p.all
	cursor   int    // index into the visible row list
	topRow   int    // first visible row; reconciled in renderRows

	// filter is the active substring filter applied to a group's
	// label-set (k=v pairs joined). preFilter is the snapshot the
	// page restores on PromptCancelledMsg per the shared
	// `/`-prompt contract (see alerts page for the lifecycle doc).
	// Filtering operates at group granularity: a group either is
	// or isn't in the rendered list; expanding a matched group
	// shows every alert it carries, unfiltered.
	filter    string
	preFilter *string
}

// New constructs an empty groups page.
func New(styles theme.Styles) *Page {
	return &Page{styles: styles}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "groups" }

// Title implements app.Page. Filtered/total shape mirrors the
// alerts page: `groups[N]` when no filter is active,
// `groups[F/T]` while one is.
func (p *Page) Title() string {
	visible := p.visibleGroups()
	if p.filter != "" {
		return fmt.Sprintf("groups[%d/%d]", len(visible), len(p.all))
	}
	return fmt.Sprintf("groups[%d]", len(visible))
}

// HeaderContent implements app.Page. Surfaces the active filter
// when one is set so the user can see what's been applied.
func (p *Page) HeaderContent() string {
	if p.filter != "" {
		return "filter:" + p.filter
	}
	return ""
}

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "expand / drill", View: "groups"},
		{Key: "s", Description: "silence group", View: "groups", Dangerous: true},
		{Key: "Tab", Description: "expand all", View: "groups"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.DataMsg:
		groups, ok := m.Resource.([]backend.AlertGroup)
		if !ok {
			return p, nil
		}
		p.all = groups
		p.expanded = make([]bool, len(groups))
		p.clampCursor()
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
			p.clampCursor()
		}
	case footer.PromptChangedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		p.filter = m.Value
		p.clampCursor()
	case footer.PromptSubmittedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		p.filter = m.Value
		p.preFilter = nil
		p.clampCursor()
	case footer.PromptCancelledMsg:
		if m.Mode != footer.PromptFilter || p.preFilter == nil {
			return
		}
		p.filter = *p.preFilter
		p.preFilter = nil
		p.clampCursor()
	}
}

// clampCursor bounds p.cursor against the post-filter row count.
func (p *Page) clampCursor() {
	if p.cursor >= len(p.rows()) {
		p.cursor = max(len(p.rows())-1, 0)
	}
}

// row is one rendered line. groupIdx points at the parent group;
// alertIdx is -1 for a group header, ≥0 for a leaf.
type row struct {
	groupIdx int
	alertIdx int
}

// rows builds the visible row list from p.all + p.expanded,
// skipping any group whose label-set doesn't match p.filter (when
// set). Leaves of an expanded matched group always appear — once
// the user expands a matched group, every alert in it shows up
// regardless of whether the alert's labels would match the filter
// in isolation.
func (p *Page) rows() []row {
	q := strings.ToLower(p.filter)
	out := make([]row, 0, len(p.all))
	for gi, g := range p.all {
		if q != "" && !strings.Contains(strings.ToLower(labelSummary(g.Labels)), q) {
			continue
		}
		out = append(out, row{groupIdx: gi, alertIdx: -1})
		if gi < len(p.expanded) && p.expanded[gi] {
			for ai := range g.Alerts {
				out = append(out, row{groupIdx: gi, alertIdx: ai})
			}
		}
	}
	return out
}

// visibleGroups returns the slice of groups whose label-set
// matches p.filter — same predicate rows() uses for headers.
func (p *Page) visibleGroups() []backend.AlertGroup {
	if p.filter == "" {
		return p.all
	}
	q := strings.ToLower(p.filter)
	out := make([]backend.AlertGroup, 0, len(p.all))
	for _, g := range p.all {
		if strings.Contains(strings.ToLower(labelSummary(g.Labels)), q) {
			out = append(out, g)
		}
	}
	return out
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	rows := p.rows()
	switch m.String() {
	case "j", "down":
		if p.cursor < len(rows)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "g":
		p.cursor = 0
	case "G":
		p.cursor = max(len(rows)-1, 0)
	case "tab":
		p.toggleExpandAll()
	case "enter":
		return p.onEnter(rows)
	case "s":
		return p.onSilence(rows)
	}
	return p, nil
}

// toggleExpandAll flips every group's expanded flag based on the
// current majority — if any group is collapsed, expand all;
// otherwise collapse all.
func (p *Page) toggleExpandAll() {
	wantExpand := false
	for _, e := range p.expanded {
		if !e {
			wantExpand = true
			break
		}
	}
	for i := range p.expanded {
		p.expanded[i] = wantExpand
	}
}

// onEnter expands / collapses a group header or drills to a leaf
// alert.
func (p *Page) onEnter(rows []row) (app.Page, tea.Cmd) {
	if p.cursor >= len(rows) {
		return p, nil
	}
	r := rows[p.cursor]
	if r.alertIdx == -1 {
		p.expanded[r.groupIdx] = !p.expanded[r.groupIdx]
		return p, nil
	}
	alert := p.all[r.groupIdx].Alerts[r.alertIdx]
	return p, func() tea.Msg { return DrillAlertMsg{Alert: alert} }
}

// onSilence emits a DrillSilenceMsg with the cursor's group's
// common-labels intersection. Cursor on a leaf still uses the
// leaf's parent group.
func (p *Page) onSilence(rows []row) (app.Page, tea.Cmd) {
	if p.cursor >= len(rows) {
		return p, nil
	}
	r := rows[p.cursor]
	if r.groupIdx >= len(p.all) {
		return p, nil
	}
	common := commonLabels(p.all[r.groupIdx].Alerts)
	return p, func() tea.Msg { return DrillSilenceMsg{CommonLabels: common} }
}

// commonLabels returns the labels that appear with the same value
// in every alert. Used by the group-silence flow so the silence
// form opens with matchers covering exactly the alerts in this
// group.
func commonLabels(alerts []backend.Alert) map[string]string {
	if len(alerts) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(alerts[0].Labels))
	maps.Copy(out, alerts[0].Labels)
	for _, a := range alerts[1:] {
		for k, v := range out {
			other, ok := a.Labels[k]
			if !ok || other != v {
				delete(out, k)
			}
		}
	}
	return out
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	rows := p.rows()
	if len(rows) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render("no groups (yet)")
	}
	maxRows := min(height, len(rows))
	p.reconcileScroll(maxRows, len(rows))
	end := min(p.topRow+maxRows, len(rows))
	out := make([]string, 0, end-p.topRow)
	for i := p.topRow; i < end; i++ {
		r := rows[i]
		out = append(out, p.renderRow(r, i == p.cursor, width))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func (p *Page) renderRow(r row, focused bool, width int) string {
	g := p.all[r.groupIdx]
	prefix := "  "
	if focused {
		prefix = "▸ "
	}
	var body string
	if r.alertIdx == -1 {
		marker := "▸"
		if p.expanded[r.groupIdx] {
			marker = "▾"
		}
		body = prefix + marker + " " + labelSummary(g.Labels) + fmt.Sprintf(" (%d alerts)", len(g.Alerts))
	} else {
		a := g.Alerts[r.alertIdx]
		body = prefix + "    " + a.Labels["alertname"] + " — " + string(a.State)
	}
	body = padRight(body, width)
	if focused {
		return p.styles.Table.Cursor.Render(body)
	}
	return body
}

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
// totalRows is the live row-count (groups can expand and shrink as
// the user toggles), so it's threaded through rather than read off
// the page.
func (p *Page) reconcileScroll(maxRows, totalRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	maxTop := max(totalRows-maxRows, 0)
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

// labelSummary renders a "k=v, k=v" preview of a label-set so the
// group header is identifiable at a glance.
func labelSummary(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + labels[k]
	}
	return strings.Join(parts, ",")
}

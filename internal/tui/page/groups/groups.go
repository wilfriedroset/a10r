// SPDX-License-Identifier: Apache-2.0

// Package groups renders the alert-groups view: a two-level tree
// where each group label-set expands to its member alerts. Enter
// on a group toggles expand/collapse; Enter on a leaf drills to
// the alert-detail page (DrillAlertMsg). `s` pushes the silence
// form prefilled with the group's common-labels intersection.
package groups

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// DrillAlertMsg is emitted on Enter against a leaf row. The wiring
// layer pushes the alert detail page.
type DrillAlertMsg struct {
	Alert backend.Alert
}

// groupEntry pairs an alert group with the tenant tag it was
// polled under so the renderer can show which backend each group
// belongs to when the active scope spans more than one tenant.
type groupEntry struct {
	g      backend.AlertGroup
	tenant string
}

// Options bundles the page's constructor inputs. Clients is the
// per-tenant write surface the page hands to the silence form on
// `s`; empty in tests / read-only runs flashes a hint instead of
// pushing a broken form. Same shape the alerts / silences pages
// consume.
type Options struct {
	Styles theme.Styles
	// Now injects the form's clock. nil falls back to time.Now in
	// the silenceform constructor.
	Now func() time.Time
	// Clients is the per-tenant write surface the page hands to
	// the silence form. Picked up by the cursor row's tenant
	// (groupEntry.tenant), set when the poller emits DataMsg.
	Clients map[string]silenceform.Client
	// Creator seeds the form's CreatedBy field; usually $USER.
	Creator string
}

// Page is the groups view.
type Page struct {
	styles theme.Styles
	now    func() time.Time

	// clients is the per-tenant write surface for `s`; see Options.
	clients map[string]silenceform.Client
	// creator seeds the silence form's CreatedBy field.
	creator string

	// byTenant holds the most recent snapshot per backend.
	byTenant map[string][]backend.AlertGroup
	// flattened cache of in-scope groups, rebuilt on every
	// recompute. Indices into this slice are what `expanded` and
	// `cursor` reference.
	flat     []groupEntry
	expanded []bool // per-group flag, indexed against p.flat
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

	// scope mirrors the active tenant scope.
	scope string

	// polledTenants / nextRefresh / refreshing / spinner mirror the
	// alerts and silences pages' polling UX so the three list pages
	// frame identically. See alerts.Page for the design notes.
	polledTenants map[string]struct{}
	nextRefresh   map[string]time.Time
	refreshing    bool
	spinner       spinner.Model
}

// scopeAll is the canonical "every configured tenant" label.
const scopeAll = "all"

// New constructs an empty groups page.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	sp := spinner.New(
		spinner.WithSpinner(spinner.Points),
		spinner.WithStyle(opts.Styles.Header.Accent),
	)
	return &Page{
		styles:        opts.Styles,
		now:           now,
		clients:       opts.Clients,
		creator:       opts.Creator,
		byTenant:      map[string][]backend.AlertGroup{},
		scope:         scopeAll,
		polledTenants: map[string]struct{}{},
		nextRefresh:   map[string]time.Time{},
		spinner:       sp,
	}
}

// Init implements app.Page. Kicks the spinner so the cold-start
// "loading" affordance animates while the first poll tick lands.
func (p *Page) Init() tea.Cmd { return p.spinner.Tick }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "groups" }

// Title implements app.Page. Mirrors the alerts shape:
// `groups(<scope>)[<count>]` or `groups(<scope>)[F/T]` while a
// filter is active. While the page is in a loading window —
// cold start (no DataMsg yet) or a manual `r` refresh in flight
// — the title flips to the spinner-led "loading groups…" so the
// border itself reads as the loading affordance, k9s-style.
func (p *Page) Title() string {
	if !p.polled() || p.refreshing {
		return p.spinner.View() + " loading groups…"
	}
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalGroups()
	visible := len(p.visibleGroups())
	if p.filter != "" {
		return fmt.Sprintf("groups(%s)[%d/%d]", scope, visible, total)
	}
	return fmt.Sprintf("groups(%s)[%d]", scope, visible)
}

// totalGroups is the in-scope count regardless of filter.
func (p *Page) totalGroups() int {
	n := 0
	for tenant, gs := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		n += len(gs)
	}
	return n
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

// HeaderContent implements app.Page. Surfaces the active filter
// when one is set so the user can see what's been applied.
func (p *Page) HeaderContent() string {
	if p.filter != "" {
		return "filter:" + p.filter
	}
	return ""
}

// Footer implements app.Page. Renders the next-refresh deadline
// — or "refreshing…" while a manual `r` is in flight — into the
// bordered body's bottom edge. Same shape as alerts / silences.
func (p *Page) Footer() string {
	if p.refreshing {
		return "refreshing…"
	}
	if !p.polled() {
		return ""
	}
	next := p.soonestNextRefresh()
	if next.IsZero() {
		return ""
	}
	return "next refresh " + nextRefreshLabel(p.now(), next)
}

// soonestNextRefresh returns the earliest in-scope DataMsg.NextAt.
func (p *Page) soonestNextRefresh() time.Time {
	var soonest time.Time
	for tenant, ts := range p.nextRefresh {
		if !p.scopeIncludes(tenant) {
			continue
		}
		if soonest.IsZero() || ts.Before(soonest) {
			soonest = ts
		}
	}
	return soonest
}

// nextRefreshLabel formats the bottom-border deadline. Past-due
// renders as "due" so a slow tick reads honestly.
func nextRefreshLabel(now, next time.Time) string {
	d := next.Sub(now)
	if d <= 0 {
		return "due"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// polled reports whether at least one in-scope tenant has produced
// a DataMsg yet. Scope-aware to avoid flickering out of loading
// state on a multi-backend setup with a narrowed scope.
func (p *Page) polled() bool {
	for tenant := range p.polledTenants {
		if p.scopeIncludes(tenant) {
			return true
		}
	}
	return false
}

// spinnerActive reports whether the spinner should keep ticking.
func (p *Page) spinnerActive() bool { return !p.polled() || p.refreshing }

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache only replays "groups" payloads into this page
// on push.
func (*Page) PollResources() []string { return []string{"groups"} }

// Bindings implements app.Page.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "expand / drill", View: "groups"},
		{Key: "s", Description: "silence group", View: "groups", Dangerous: true},
		{Key: "Tab", Description: "expand all", View: "groups"},
		{Key: "r", Description: "refresh", View: "groups"},
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
		p.byTenant[m.Tenant] = groups
		if !m.NextAt.IsZero() {
			p.nextRefresh[m.Tenant] = m.NextAt
		}
		p.polledTenants[m.Tenant] = struct{}{}
		if p.scopeIncludes(m.Tenant) {
			p.refreshing = false
		}
		p.recompute()
		return p, nil
	case spinner.TickMsg:
		if !p.spinnerActive() {
			return p, nil
		}
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(m)
		return p, cmd
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return p, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash so the user sees
		// confirmation. Same shape alerts / silences use.
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
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

// recompute rebuilds p.flat from byTenant + scope, preserving any
// in-place expanded flags by group identity (label-set + tenant)
// across refresh ticks. New groups land collapsed; vanished
// groups simply drop out.
func (p *Page) recompute() {
	prev := make(map[string]bool, len(p.flat))
	for i, e := range p.flat {
		prev[groupKey(e)] = i < len(p.expanded) && p.expanded[i]
	}
	p.flat = p.flat[:0]
	for tenant, gs := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, g := range gs {
			p.flat = append(p.flat, groupEntry{g: g, tenant: tenant})
		}
	}
	p.expanded = make([]bool, len(p.flat))
	for i, e := range p.flat {
		if prev[groupKey(e)] {
			p.expanded[i] = true
		}
	}
	p.clampCursor()
}

// groupKey uniquely identifies a group across refreshes: tenant +
// sorted label-set. Used to preserve expanded state when the
// underlying slice ordering changes between polls.
func groupKey(e groupEntry) string {
	return e.tenant + "\x00" + labelSummary(e.g.Labels)
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

// rows builds the visible row list from p.flat + p.expanded,
// skipping any group whose label-set doesn't match p.filter (when
// set). Leaves of an expanded matched group always appear — once
// the user expands a matched group, every alert in it shows up
// regardless of whether the alert's labels would match the filter
// in isolation.
func (p *Page) rows() []row {
	q := strings.ToLower(p.filter)
	out := make([]row, 0, len(p.flat))
	for gi, e := range p.flat {
		if q != "" && !strings.Contains(strings.ToLower(labelSummary(e.g.Labels)), q) {
			continue
		}
		out = append(out, row{groupIdx: gi, alertIdx: -1})
		if gi < len(p.expanded) && p.expanded[gi] {
			for ai := range e.g.Alerts {
				out = append(out, row{groupIdx: gi, alertIdx: ai})
			}
		}
	}
	return out
}

// visibleGroups returns the slice of in-scope groups whose
// label-set matches p.filter — same predicate rows() uses for
// headers.
func (p *Page) visibleGroups() []backend.AlertGroup {
	if p.filter == "" {
		out := make([]backend.AlertGroup, len(p.flat))
		for i, e := range p.flat {
			out[i] = e.g
		}
		return out
	}
	q := strings.ToLower(p.filter)
	out := make([]backend.AlertGroup, 0, len(p.flat))
	for _, e := range p.flat {
		if strings.Contains(strings.ToLower(labelSummary(e.g.Labels)), q) {
			out = append(out, e.g)
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
	case "r":
		cmd := p.requestRefresh()
		return p, cmd
	}
	return p, nil
}

// requestRefresh emits RefreshRequestedMsg and re-arms the
// spinner. Same shape as alerts / silences.
func (p *Page) requestRefresh() tea.Cmd {
	p.refreshing = true
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "groups", Scope: scope}
	}
	return tea.Batch(emit, p.spinner.Tick)
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
	alert := p.flat[r.groupIdx].g.Alerts[r.alertIdx]
	return p, func() tea.Msg { return DrillAlertMsg{Alert: alert} }
}

// onSilence pushes the silence form prefilled with the cursor
// group's common-labels intersection (`__name__` dropped). The
// cursor on a leaf row still uses the leaf's parent group — so
// silencing a single alert requires drilling into it first via
// Enter, then `s` on the detail page; `s` from the groups view
// always covers every alert in the group.
func (p *Page) onSilence(rows []row) (app.Page, tea.Cmd) {
	if p.cursor >= len(rows) {
		return p, flashFn(footer.FlashInfo, "no group under the cursor")
	}
	r := rows[p.cursor]
	if r.groupIdx >= len(p.flat) {
		return p, flashFn(footer.FlashInfo, "no group under the cursor")
	}
	entry := p.flat[r.groupIdx]
	if len(p.clients) == 0 {
		return p, flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	client, ok := p.clients[entry.tenant]
	if !ok {
		return p, flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	matchers := silenceform.MatchersFromLabels(commonLabels(entry.g.Alerts))
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	return p, app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Client:   client,
			Styles:   styles,
			Now:      now,
			Creator:  creator,
			Matchers: matchers,
		})
	})
}

// flashFn returns a Cmd emitting a FlashShowMsg. Tiny helper so
// the action handlers stay one-liners.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

// hintNoWriteableBackend mirrors the alerts / alert page consts
// so the "configure a writeable backend" hint reads identically
// across the three pages that push the silence form on `s`.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

// emptyState picks the right body for an empty list. The cold-
// start / refresh-in-flight loading hint lives in the title; the
// body stays empty in that window so there's no duplicate
// spinner. After the first DataMsg lands and there's genuinely
// nothing to show, the body explains why.
func (p *Page) emptyState() string {
	if !p.polled() || p.refreshing {
		return ""
	}
	if p.filter != "" {
		return "no groups match the active filter — Esc clears the prompt"
	}
	return "no groups (yet)"
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
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		return lipgloss.NewStyle().Width(width).Height(height).Render(p.emptyState())
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
	entry := p.flat[r.groupIdx]
	prefix := "  "
	if focused {
		prefix = "▸ "
	}
	// Per Q5.3: leading TENANT column on group header rows only,
	// when scope==all and at least two in-scope tenants are present.
	// Leaf rows skip the column — the parent header already names
	// the source backend.
	tenantPrefix := ""
	if p.showTenantColumn() && r.alertIdx == -1 {
		tenantPrefix = padRight(entry.tenant, tenantColWidth) + "  "
	}
	var body string
	if r.alertIdx == -1 {
		marker := "▸"
		if p.expanded[r.groupIdx] {
			marker = "▾"
		}
		summary := labelSummary(entry.g.Labels)
		if !focused {
			// Cursor row wraps the whole line in fg+bg per Q5.4 / the
			// alerts page convention; nested ANSI inside the wrap is
			// fragile, so the per-cell colouring is skipped on the
			// cursor row.
			summary = styledLabelSummary(entry.g.Labels, p.styles)
		}
		body = prefix + tenantPrefix + marker + " " + summary +
			fmt.Sprintf(" (%d alerts)", len(entry.g.Alerts))
	} else {
		a := entry.g.Alerts[r.alertIdx]
		alertname := a.Labels["alertname"]
		state := string(a.State)
		if !focused {
			alertname = p.styles.YAML.Key.Render(alertname)
			state = p.styles.YAML.Value.Render(state)
		}
		body = prefix + "    " + alertname + " — " + state
	}
	body = padRight(body, width)
	if focused {
		return p.styles.Table.Cursor.Render(body)
	}
	return body
}

// tenantColWidth is the fixed width of the leading TENANT column
// on group header rows in multi-tenant scope. Mirrors the alerts
// / silences pages so the three list views align visually when
// the user switches between them.
const tenantColWidth = 16

// showTenantColumn reports whether the renderer should prefix
// group header rows with the TENANT column. True when scope
// spans more than one in-scope tenant. Same predicate as the
// alerts / silences pages — Q5.3 confirmed.
func (p *Page) showTenantColumn() bool {
	if p.scope != scopeAll {
		return false
	}
	in := 0
	for tenant := range p.byTenant {
		if p.scopeIncludes(tenant) {
			in++
		}
	}
	return in > 1
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
// group header is identifiable at a glance. Plain-text variant
// kept for filter matching (lower-cased substring search needs
// the unstyled string) and as the cursor-row body where the
// row-level fg+bg wrap supersedes per-cell colouring.
func labelSummary(labels map[string]string) string {
	keys := sortedLabelKeys(labels)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + labels[k]
	}
	return strings.Join(parts, ",")
}

// styledLabelSummary returns the same `k=v, k=v` preview with the
// label name rendered in theme.YAML.Key and the value in
// theme.YAML.Value — matches the YAML viewer's colouring so the
// k=v pair reads consistently across the TUI. Punctuation (= and
// ,) uses theme.YAML.Punct so the visual hierarchy is name >
// value > separator. Per Q5.2.
func styledLabelSummary(labels map[string]string, styles theme.Styles) string {
	keys := sortedLabelKeys(labels)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = styles.YAML.Key.Render(k) +
			styles.YAML.Punct.Render("=") +
			styles.YAML.Value.Render(labels[k])
	}
	return strings.Join(parts, styles.YAML.Punct.Render(","))
}

// sortedLabelKeys returns the keys of labels in deterministic
// alphabetical order. Pulled out so labelSummary and
// styledLabelSummary share the ordering rule — diverging
// orderings would make the styled vs plain output disagree on
// how a group reads.
func sortedLabelKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

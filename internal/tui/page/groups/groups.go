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
	"strconv"
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
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// DrillAlertMsg is emitted on Enter against a leaf row. The wiring
// layer pushes the alert detail page.
type DrillAlertMsg struct {
	Alert backend.Alert
}

// Sort column keys. Stable identifiers passed to the tablesort
// helper — the title slug ("name", "count", "severity") matches
// what the help overlay renders ("sort by <title>").
const (
	sortKeyName     = "name"
	sortKeyCount    = "count"
	sortKeySeverity = "severity"
)

// groupSortColumns returns the page's sortable axes. Count and
// severity default DESC (noisiest / worst groups land first —
// triage priority); name defaults ASC (alphabetical reading
// order). Ties on count / severity fall back to name-asc so the
// ordering stays deterministic across refreshes.
func groupSortColumns() []tablesort.Column[groupEntry] {
	nameLess := func(a, b groupEntry) bool {
		return labelSummary(a.g.Labels) < labelSummary(b.g.Labels)
	}
	return []tablesort.Column[groupEntry]{
		{
			Key: sortKeyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true,
			Less: nameLess,
		},
		{
			Key: sortKeyCount, Title: "COUNT", Hotkey: 'C', DefaultAsc: false,
			Description: "sort by alert count",
			Less: func(a, b groupEntry) bool {
				ai, bi := len(a.g.Alerts), len(b.g.Alerts)
				if ai != bi {
					return ai < bi
				}
				return nameLess(a, b)
			},
		},
		{
			// `s` is the silence verb on this page; severity uses
			// Shift+V (mnemonic for "severity") to dodge the
			// collision.
			Key: sortKeySeverity, Title: "SEVERITY", Hotkey: 'V', DefaultAsc: false,
			Less: func(a, b groupEntry) bool {
				if a.severityRank != b.severityRank {
					return a.severityRank < b.severityRank
				}
				return nameLess(a, b)
			},
		},
	}
}

// groupEntry pairs an alert group with the tenant tag it was
// polled under so the renderer can show which backend each group
// belongs to when the active scope spans more than one tenant.
//
// severityRank caches the worst-severity rank across the group's
// alerts so the severity comparator doesn't walk the alert slice
// on every comparison; recompute populates it once per poll. Zero
// is the natural rest value (no severity → unknown).
//
// common caches the labels shared by every alert in the group so
// leaf rendering can compute distinguishing labels in O(alert) per
// frame rather than O(alert × group) by re-deriving the
// intersection on every render.
type groupEntry struct {
	g            backend.AlertGroup
	tenant       string
	severityRank int
	common       map[string]string
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

	// bodyHeight is the row capacity snapshotted on the most recent
	// View. Ctrl+D / Ctrl+U step half this; Ctrl+F / Ctrl+B step
	// body-2 (vim's CTRL-F two-line overlap convention). Zero before
	// the first render — handlers fall back to 10 / 20 so a keystroke
	// that beats the initial WindowSizeMsg still moves a sane distance.
	bodyHeight int

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

	// sorter owns the active sort axis and direction. Default:
	// name ASC — alphabetical label-set, matching the historical
	// implicit ordering. Mirrors the alerts / silences page state
	// shape so handleSort can be the same shape across the three
	// list pages.
	sorter *tablesort.Sorter[groupEntry]

	// focusKey is the identity of the row the cursor was on before
	// the most recent recompute — used to restore the cursor onto
	// the same logical row after a re-sort, scope change, or poll
	// refresh. Without it, pressing Shift+C would jump to whatever
	// group lands at the cursor's numeric index. Mirrors
	// focusFingerprint / focusID on alerts / silences.
	focusKey string

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
		sorter:        tablesort.New(groupSortColumns(), sortKeyName),
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

// Bindings implements app.Page. Sort shortcuts come from the
// tablesort helper so all list pages emit them identically; the
// page contributes the page-specific verbs (Enter / s / Tab / r)
// around them.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("groups")
	out := make([]action.Action, 0, 4+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "expand / drill", View: "groups"},
		action.Action{Key: "s", Description: "silence group", View: "groups", Dangerous: true},
		action.Action{Key: "Tab", Description: "expand all", View: "groups"},
	)
	out = append(out, sortBindings...)
	out = append(out, action.Action{Key: "r", Description: "refresh", View: "groups"})
	return out
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
		p.snapshotFocus()
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
// groups simply drop out. Sort is applied after the slice is
// rebuilt so the active sort column + direction govern row order.
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
			p.flat = append(p.flat, groupEntry{
				g:            g,
				tenant:       tenant,
				severityRank: groupSeverityRank(g),
				common:       commonLabels(g.Alerts),
			})
		}
	}
	p.sorter.Apply(p.flat)
	p.expanded = make([]bool, len(p.flat))
	for i, e := range p.flat {
		if prev[groupKey(e)] {
			p.expanded[i] = true
		}
	}
	// Resolve cursor by focusKey when we have one to follow so the
	// user stays on the same logical row across re-sort / scope /
	// poll. Falls through to the clamp + re-snapshot path when
	// focus is empty or the focused row vanished.
	if p.focusKey != "" {
		rows := p.rows()
		for i, r := range rows {
			if rowKey(p.flat, r) == p.focusKey {
				p.cursor = i
				return
			}
		}
	}
	p.clampCursor()
	p.snapshotFocus()
}

// rowKey builds the focus identity for r. Group headers use the
// stable groupKey; leaf rows append the alertname so the cursor
// can ride a specific alert across refreshes even when its
// position inside the group changes.
func rowKey(flat []groupEntry, r row) string {
	if r.groupIdx < 0 || r.groupIdx >= len(flat) {
		return ""
	}
	g := flat[r.groupIdx]
	if r.alertIdx < 0 {
		return groupKey(g)
	}
	if r.alertIdx >= len(g.g.Alerts) {
		return groupKey(g)
	}
	return groupKey(g) + "\x00" + g.g.Alerts[r.alertIdx].Labels["alertname"]
}

// snapshotFocus captures the rowKey of the row currently under
// the cursor so the next recompute can re-resolve it. Empty
// view leaves focus empty.
func (p *Page) snapshotFocus() {
	rows := p.rows()
	if p.cursor < 0 || p.cursor >= len(rows) {
		p.focusKey = ""
		return
	}
	p.focusKey = rowKey(p.flat, rows[p.cursor])
}

// groupSeverityRank returns the worst severity in g, encoded so
// higher = more severe. Reuses backend.SeverityRank so the
// alerts page and the groups page agree on what "worst" means.
func groupSeverityRank(g backend.AlertGroup) int {
	worst := 0
	for _, a := range g.Alerts {
		if r := backend.SeverityRank(a); r > worst {
			worst = r
		}
	}
	return worst
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
	if p.handleSort(m) {
		return p, nil
	}
	rows := p.rows()
	// `g` alone is dead code — the dispatcher's chord buffer at
	// LayerTable consumes the first `g` waiting for the second. The
	// chord-completed `gg` arrives as app.GoToFirstRowMsg and is
	// handled in Update.
	if newCursor, handled := cursor.HandleMotion(
		m.String(),
		p.cursor,
		len(rows),
		cursor.HalfPageStep(p.bodyHeight),
		cursor.FullPageStep(p.bodyHeight),
	); handled {
		p.cursor = newCursor
		p.snapshotFocus()
		return p, nil
	}
	switch m.String() {
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

// handleSort processes sort-axis shortcuts (h/l walk plus
// Shift+letter direct shortcuts). Returns true when the key was a
// sort change so the caller skips its other branches. Same shape
// as alerts / silences so the three list pages stay aligned.
//
// `s` is the silence verb on this page, so the severity sort
// uses Shift+V (mnemonic for "severity"), avoiding a collision
// with the existing `s` action handler.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	return cursor.HandleSort(
		m.String(),
		p.sorter,
		func() { p.focusKey = "" },
		p.recompute,
	)
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

// distinguishingLabels returns the labels in a that aren't shared
// across every sibling — i.e. the keys whose value diverges from
// the group's commonLabels intersection. Renders on leaf rows so
// each leaf identifies the actual instance (instance / pod /
// host / …) rather than echoing the labels already painted in the
// group header.
func distinguishingLabels(a backend.Alert, common map[string]string) map[string]string {
	out := make(map[string]string, len(a.Labels))
	for k, v := range a.Labels {
		if cv, ok := common[k]; ok && cv == v {
			continue
		}
		out[k] = v
	}
	return out
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
	p.bodyHeight = height - 1 // header takes one line; the rest is row budget
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
	maxRows := min(height-1, len(rows))
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, maxRows, len(rows))
	end := min(p.topRow+maxRows, len(rows))
	out := make([]string, 0, end-p.topRow+1)
	out = append(out, p.renderHeader(width))
	for i := p.topRow; i < end; i++ {
		r := rows[i]
		out = append(out, p.renderRow(r, i == p.cursor, width))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

// renderHeader emits the column-title row. NAME / COUNT /
// SEVERITY sit above their respective data columns; the active
// axis carries an `↑` (ASC) or `↓` (DESC) arrow per the alerts /
// silences convention. A leading TENANT slot appears when scope
// spans multiple in-scope backends.
func (p *Page) renderHeader(width int) string {
	tenantW, nameW, countW, sevW := p.columnWidths(width)
	// fg-only so the header keeps the terminal default background
	// — painted palette bg in the unstyled body frame creates a
	// coloured stripe.
	headerFg := theme.FgOnly(p.styles.Table.Header.GetForeground())
	activeFg := theme.FgOnly(p.styles.Table.HeaderActive.GetForeground())

	render := func(label, key string, w int) string {
		if arrow := p.sorter.ArrowFor(key); arrow != "" {
			label = label + " " + arrow
		}
		if p.sorter.IsActive(key) {
			return activeFg.Render(format.PadRight(label, w))
		}
		return headerFg.Render(format.PadRight(label, w))
	}

	var b strings.Builder
	if tenantW > 0 {
		b.WriteString(headerFg.Render(format.PadRight("TENANT", tenantW)))
	}
	// Tree-marker column has no header label — it carries ▸/▾ on
	// data rows only.
	b.WriteString(strings.Repeat(" ", treeColWidth))
	b.WriteString(render("NAME", sortKeyName, nameW))
	b.WriteString(render("COUNT", sortKeyCount, countW))
	b.WriteString(render("SEVERITY", sortKeySeverity, sevW))
	return format.PadRight(b.String(), width)
}

func (p *Page) renderRow(r row, focused bool, width int) string {
	entry := p.flat[r.groupIdx]
	tenantW, nameW, countW, sevW := p.columnWidths(width)

	var b strings.Builder
	// Leading TENANT slot: present on every row when the scope
	// spans multiple tenants so columns line up regardless of
	// row kind. Group rows fill it; leaf rows leave it blank
	// since the parent header already names the source backend.
	if tenantW > 0 {
		if r.alertIdx == -1 {
			b.WriteString(format.PadRight(entry.tenant, tenantW))
		} else {
			b.WriteString(strings.Repeat(" ", tenantW))
		}
	}

	if r.alertIdx == -1 {
		marker := "▸"
		if p.expanded[r.groupIdx] {
			marker = "▾"
		}
		b.WriteString(marker + " ")

		summary := labelSummary(entry.g.Labels)
		if !focused {
			// Cursor row wraps the whole line in fg+bg per Q5.4 / the
			// alerts page convention; nested ANSI inside the wrap is
			// fragile, so the per-cell colouring is skipped on the
			// cursor row.
			summary = styledLabelSummary(entry.g.Labels, p.styles)
		}
		b.WriteString(format.PadRight(summary, nameW))

		count := strconv.Itoa(len(entry.g.Alerts))
		b.WriteString(format.PadRight(count, countW))

		sev := severityLabelByRank(entry.severityRank)
		if !focused {
			sev = severityStyleByRank(entry.severityRank, p.styles).Render(sev)
		}
		b.WriteString(format.PadRight(sev, sevW))
	} else {
		// Leaf row: empty tree slot, then the labels that
		// distinguish this leaf from its siblings (instance, pod,
		// host, …) plus its state. The group header already shows
		// the labels common to every alert; echoing them on every
		// leaf is dead pixels and hides the field that actually
		// identifies the instance. Falls back to the alertname
		// when distinguishing labels are empty (true duplicates)
		// so the row never reads as blank.
		b.WriteString(strings.Repeat(" ", treeColWidth))
		a := entry.g.Alerts[r.alertIdx]
		diff := distinguishingLabels(a, entry.common)
		var labelText string
		if len(diff) > 0 {
			if focused {
				labelText = labelSummary(diff)
			} else {
				labelText = styledLabelSummary(diff, p.styles)
			}
		} else {
			labelText = a.Labels["alertname"]
			if !focused {
				labelText = p.styles.YAML.Key.Render(labelText)
			}
		}
		state := string(a.State)
		if !focused {
			state = p.styles.YAML.Value.Render(state)
		}
		// Leaves collapse the NAME / COUNT / SEVERITY columns into
		// one wide cell on purpose: a leaf has no group-level count
		// and its severity is already rolled into the parent's
		// SEVERITY cell, so per-leaf alignment under those columns
		// would be padding around dead air. Drilling via Enter is
		// where per-alert detail belongs.
		leaf := "  " + labelText + " — " + state
		b.WriteString(format.PadRight(leaf, nameW+countW+sevW))
	}

	body := format.PadRight(b.String(), width)
	if focused {
		// k9s parity: cursor bg tracks the row's semantic colour
		// (max severity for groups) rather than the static
		// cursorBgColor — see k9s select_table.go:128.
		rowColor := severityStyleByRank(entry.severityRank, p.styles).GetForeground()
		return p.styles.Table.CursorOver(rowColor).Render(body)
	}
	return body
}

// Column geometry. tenantColWidth mirrors the alerts / silences
// pages so the three views align across page switches.
// treeColWidth reserves space for the ▸/▾ marker plus a trailing
// space; countColWidth fits up to a 9-digit count, severityColWidth
// fits the longest severity label ("critical") with breathing room.
const (
	tenantColWidth   = 16
	treeColWidth     = 2
	countColWidth    = 10
	severityColWidth = 12
)

// columnWidths returns the rendered widths for the optional TENANT
// column, then NAME (flex), COUNT, and SEVERITY. NAME absorbs the
// remainder so the layout fills the body width without truncating
// the fixed cells. Floored at 10 so a narrow terminal still leaves
// the labels readable rather than collapsing the NAME column to
// zero.
func (p *Page) columnWidths(width int) (tenant, name, count, sev int) {
	if p.showTenantColumn() {
		tenant = tenantColWidth
	}
	count = countColWidth
	sev = severityColWidth
	name = max(width-tenant-treeColWidth-count-sev, 10)
	return tenant, name, count, sev
}

// severityLabelByRank inverts backend.SeverityRank to the printable
// label so the SEVERITY column reads consistently with the alerts
// page's per-row severity cell. Unknown rank renders as `—` to
// match alerts.severityOf.
func severityLabelByRank(rank int) string {
	switch rank {
	case 3:
		return "critical"
	case 2:
		return "warning"
	case 1:
		return "info"
	}
	return "—"
}

// severityStyleByRank picks the lipgloss style for the SEVERITY
// cell, mirroring alerts.severityStyle so a "critical" cell tints
// the same on both pages.
func severityStyleByRank(rank int, styles theme.Styles) lipgloss.Style {
	switch rank {
	case 3:
		return styles.Severity.Critical
	case 2:
		return styles.Severity.Warning
	case 1:
		return styles.Severity.Info
	}
	return styles.Severity.Unknown
}

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

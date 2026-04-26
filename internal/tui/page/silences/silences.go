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
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
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

// silenceEntry pairs a silence with the tenant tag the poller
// emitted it under so the renderer can surface a TENANT column
// when more than one backend's data is in scope.
type silenceEntry struct {
	s      backend.Silence
	tenant string
}

// Page is the silences list view.
type Page struct {
	styles theme.Styles
	now    func() time.Time

	// byTenant holds the most recent snapshot for each backend
	// keyed by the poll.DataMsg.Tenant tag. Pages built in single-
	// backend setups end up with one entry; multi-backend ones
	// accumulate every snapshot they've received.
	byTenant map[string][]backend.Silence
	view     []silenceEntry
	cursor   int

	// topRow keeps the cursor inside the visible window — see
	// reconcileScroll. Lazily updated by the renderer because the
	// body height (and therefore the row budget) is only known at
	// render time.
	topRow int

	sort    SortKey
	sortAsc bool
	focusID string

	// filter is the active substring filter (creator / matcher
	// fields / comment). preFilter is the snapshot the page
	// restores on PromptCancelledMsg{Mode: PromptFilter}; nil iff
	// no filter prompt is open. Same shape as the alerts page.
	filter    string
	preFilter *string

	// scope is the active tenant scope ("all", a single backend
	// name, or comma-joined names). Mirrors what the alerts page
	// tracks and is updated by app.ScopeChangedMsg.
	scope string

	// clients are the per-tenant write surfaces the page hands to
	// the silence form when the user presses `n`. Empty in tests
	// or read-only runs — write actions flash a hint instead.
	clients map[string]silenceform.Client
	// creator seeds the form's CreatedBy field; usually $USER.
	creator string
}

// Options bundles the page's constructor inputs. Clients is the
// per-tenant write surface; the silences page picks the right one
// when the user presses `n` based on the cursor row's tenant or
// (on an empty list) the first in-scope backend. Empty Clients
// flashes a hint instead of pushing the form so a no-config or
// read-only run doesn't crash.
type Options struct {
	Styles  theme.Styles
	Now     func() time.Time
	Clients map[string]silenceform.Client
	// Creator is the default value the silence form opens with —
	// usually $USER. Empty falls back to "a10r".
	Creator string
}

// New constructs an empty silences page.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Page{
		styles:   opts.Styles,
		now:      now,
		clients:  opts.Clients,
		creator:  opts.Creator,
		byTenant: map[string][]backend.Silence{},
		sort:     SortByEndsAt,
		sortAsc:  true, // soonest-expiring first
		scope:    scopeAll,
	}
}

// scopeAll is the canonical "every configured tenant" label.
// Pinned as a constant so the title, scopeIncludes, and the
// global numeric quick-switch agree on the spelling.
const scopeAll = "all"

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "silences" }

// Title implements app.Page. Mirrors the alerts page's shape:
// `silences(<scope>)[<count>]` or `silences(<scope>)[F/T]` while
// a filter is active.
func (p *Page) Title() string {
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalSilences()
	if p.filter != "" {
		return fmt.Sprintf("silences(%s)[%d/%d]", scope, len(p.view), total)
	}
	return fmt.Sprintf("silences(%s)[%d]", scope, total)
}

// totalSilences is the unfiltered silence count within the active
// scope — same role as the alerts page's totalAlerts.
func (p *Page) totalSilences() int {
	n := 0
	for tenant, sils := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		n += len(sils)
	}
	return n
}

// scopeIncludes reports whether tenant should appear in the view.
// Empty / "all" includes everyone; otherwise the scope is matched
// against the comma-joined list (so a Ctrl+T multi-select like
// "prod,staging" lights up both backends).
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

// showTenantColumn reports whether the view spans more than one
// in-scope tenant — TENANT column is rendered iff so.
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

// HeaderContent implements app.Page. Sort indicator lives on the
// column header arrow; count lives in Title. Surface the active
// filter when one is set so the user can spot what's been
// applied without re-opening the prompt.
func (p *Page) HeaderContent() string {
	if p.filter != "" {
		return "filter:" + p.filter
	}
	return ""
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
		p.byTenant[m.Tenant] = s
		p.recompute()
		return p, nil
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return p, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		p.snapshotFocus()
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash the new silence ID so
		// the user has visual confirmation. The next poll tick
		// will surface the silence in the list.
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. No flash — form Esc is a
		// non-event from the user's perspective.
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

// handleFilterPrompt mirrors the alerts page's handler — see
// internal/tui/page/alerts/alerts.go for the full doc. Briefly:
// open snapshots and clears, change applies live, submit commits,
// cancel restores. Only filter-mode messages affect state.
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

// handleSort processes sort-column shortcuts. Same column twice
// flips ASC↔DESC; switching to a new column resets direction to
// that column's default — matching the spreadsheet-style UX.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "shift+e", "E":
		p.applySort(SortByEndsAt)
	case "shift+s", "S":
		p.applySort(SortByStartsAt)
	case "shift+c", "C":
		p.applySort(SortByCreatedBy)
	case "shift+t", "T":
		p.applySort(SortByState)
	default:
		return false
	}
	return true
}

// applySort updates sort key and direction. Same key twice flips
// ASC↔DESC; new key resets to that column's default direction.
func (p *Page) applySort(k SortKey) {
	if p.sort == k {
		p.sortAsc = !p.sortAsc
	} else {
		p.sort = k
		p.sortAsc = defaultAsc(k)
	}
	p.recompute()
}

// defaultAsc returns the direction the column reads naturally
// when first activated. EndsAt is ASC so soonest-expiring shows
// first (the operator-priority "what's about to come back");
// everything else is also ASC for consistency.
func defaultAsc(_ SortKey) bool { return true }

func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "n":
		cmd := p.openNewSilenceForm()
		return p, cmd
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

// openNewSilenceForm pushes an empty silence form targeting the
// best-fit backend. Selection rule: the cursor row's tenant
// (when a row is focused), else the first in-scope tenant from
// p.clients in alphabetical order. Empty p.clients (no backends
// configured, or read-only run) flashes a hint instead.
func (p *Page) openNewSilenceForm() tea.Cmd {
	tenant, client, ok := p.pickWriteTarget()
	if !ok {
		return flashFn(footer.FlashWarn, "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`")
	}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	now := p.now
	styles := p.styles
	_ = tenant // captured by client; reserved for a future title
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Client:  client,
			Styles:  styles,
			Now:     now,
			Creator: creator,
		})
	})
}

// pickWriteTarget returns the tenant + client to send a write to.
// Cursor row's tenant wins when a row is focused; otherwise falls
// back to the first in-scope tenant (alphabetical for stability).
// Returns (_, _, false) when nothing usable is configured.
func (p *Page) pickWriteTarget() (string, silenceform.Client, bool) {
	if len(p.clients) == 0 {
		return "", nil, false
	}
	if p.cursor < len(p.view) {
		t := p.view[p.cursor].tenant
		if c, ok := p.clients[t]; ok {
			return t, c, true
		}
	}
	names := make([]string, 0, len(p.clients))
	for t := range p.clients {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		if p.scopeIncludes(t) {
			return t, p.clients[t], true
		}
	}
	return "", nil, false
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
	rows := p.renderRows(width, height-1)
	body := headerLine + "\n" + rows
	return lipgloss.NewStyle().Width(width).Render(body)
}

func (p *Page) emptyState() string {
	if p.totalSilences() == 0 {
		return "no silences (yet)"
	}
	return "no silences in view"
}

// renderHeader returns the styled, uppercased column-title row
// with a sort marker on the active column. theme.Table.Header
// applies the k9s-style yellow header colour. When the active
// scope spans more than one tenant, a leading TENANT column is
// inserted so the user knows which backend each row came from.
func (p *Page) renderHeader(width int) string {
	titles := []SortKey{SortByEndsAt, SortByStartsAt, SortByCreatedBy, SortByState}
	parts := make([]string, 0, len(titles)+1)
	if p.showTenantColumn() {
		parts = append(parts, "TENANT")
	}
	for _, k := range titles {
		label := strings.ToUpper(k.String())
		if k == p.sort {
			arrow := "↓"
			if p.sortAsc {
				arrow = "↑"
			}
			label = label + " " + arrow
		}
		parts = append(parts, label)
	}
	// Foreground-only render so the header row keeps the body
	// background — flush with the data rows underneath rather
	// than a coloured stripe.
	return lipgloss.NewStyle().
		Foreground(p.styles.Table.Header.GetForeground()).
		Render(p.padColumns(parts, width))
}

func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.view))
	var b strings.Builder
	for i := p.topRow; i < end; i++ {
		e := p.view[i]
		row := make([]string, 0, 5)
		if p.showTenantColumn() {
			row = append(row, e.tenant)
		}
		row = append(row,
			header.FormatAge(p.now(), e.s.EndsAt),
			header.FormatAge(p.now(), e.s.StartsAt),
			e.s.CreatedBy,
			string(e.s.State),
		)
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		// Pad to the full width before styling so the Cursor row's
		// background extends across the whole line k9s-style.
		line := padRight(prefix+p.padColumns(row, width), width)
		if i == p.cursor {
			line = p.styles.Table.Cursor.Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
// Same shape as the alerts page; replicated rather than shared so
// each page stays self-contained until a tablekit emerges.
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

// padColumns lays out a row across fixed-width columns. The
// optional leading TENANT column shrinks the flex CreatedBy
// column so the totals still fit the available width.
func (p *Page) padColumns(parts []string, width int) string {
	const (
		tenantW = 16
		endsW   = 14
		startsW = 14
		stateW  = 12
		minBy   = 10
	)
	used := endsW + startsW + stateW + 2
	cols := make([]int, 0, 5)
	if p.showTenantColumn() {
		cols = append(cols, tenantW)
		used += tenantW
	}
	flex := max(width-used, minBy)
	cols = append(cols, endsW, startsW, flex, stateW)
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

// recompute rebuilds p.view by walking byTenant, applying the
// scope and substring filters, then sorting. Cursor is preserved
// across rebuilds by silence ID when possible.
func (p *Page) recompute() {
	flat := make([]silenceEntry, 0)
	for tenant, sils := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, s := range sils {
			flat = append(flat, silenceEntry{s: s, tenant: tenant})
		}
	}
	p.view = filterSilences(flat, p.filter)
	sortSilences(p.view, p.sort, p.sortAsc)
	if p.focusID != "" {
		for i, e := range p.view {
			if e.s.ID == p.focusID {
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

// filterSilences returns a fresh slice with the entries whose
// rendered text contains the lowercased query as a substring.
// Empty filter returns the input unchanged.
func filterSilences(in []silenceEntry, query string) []silenceEntry {
	if query == "" {
		return in
	}
	q := strings.ToLower(query)
	out := make([]silenceEntry, 0, len(in))
	for _, e := range in {
		if silenceMatches(e.s, q) {
			out = append(out, e)
		}
	}
	return out
}

// silenceMatches walks the user-visible text fields. The query
// caller must already be lowercased.
func silenceMatches(s backend.Silence, q string) bool {
	if strings.Contains(strings.ToLower(s.CreatedBy), q) ||
		strings.Contains(strings.ToLower(s.Comment), q) ||
		strings.Contains(strings.ToLower(string(s.State)), q) {
		return true
	}
	for _, m := range s.Matchers {
		if strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.Value), q) {
			return true
		}
	}
	return false
}

func (p *Page) snapshotFocus() {
	if p.cursor < len(p.view) {
		p.focusID = p.view[p.cursor].s.ID
		return
	}
	p.focusID = ""
}

func sortSilences(out []silenceEntry, key SortKey, asc bool) {
	less := lessFor(key)
	sort.SliceStable(out, func(i, j int) bool {
		if asc {
			return less(out[i].s, out[j].s)
		}
		return less(out[j].s, out[i].s)
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
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

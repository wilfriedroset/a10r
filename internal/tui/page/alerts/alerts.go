// SPDX-License-Identifier: Apache-2.0

// Package alerts renders the alerts list page — the home view of
// the TUI per A1 / k9s-look-and-feel.md §3. v0.1 ships a minimal
// table:
//
//   - Vim motions (j/k/g/G/Ctrl+D/Ctrl+U) plus arrow keys.
//   - Substring filter via the `/` prompt (App routes
//     PromptSubmittedMsg{PromptFilter} to the page).
//   - Severity / alertname / instance / age columns.
//   - Per E2 sort cycling by `Shift+S` (severity), `Shift+N`
//     (alertname), `Shift+T` (state), `Shift+R` (receiver). `h`/`l`
//     walk between sort columns.
//   - `s` opens the silence form (placeholder until #30); the
//     binding is filtered out under read-only mode (C4) by the
//     action registry.
//
// Polling lives in the wiring layer (cmd/tui.go in #39): a poll
// loop emits DataMsg{Resource: []backend.Alert} that this page
// consumes via Update.
package alerts

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
	"github.com/wilfriedroset/a10r/internal/tui/page/alert"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// SortKey enumerates the table's sortable columns. The order of
// constants is the cycle order for `h`/`l` walking per E2.
type SortKey int

const (
	// SortBySeverity is the default — critical first, then warning,
	// then info, then unknown.
	SortBySeverity SortKey = iota
	// SortByName sorts by `alertname` label, ascending.
	SortByName
	// SortByState sorts by AlertState (active > suppressed >
	// unprocessed).
	SortByState
	// SortByAge sorts by StartsAt, oldest first.
	SortByAge
)

// scopeAll is the canonical label for the "every configured
// tenant" scope. Used by Title, scopeIncludes, and the
// `<0>` quick-switch payload — pinning it as a constant keeps
// the wiring layer and the page in lockstep.
const scopeAll = "all"

// String returns the column-header label for a sort key.
func (s SortKey) String() string {
	switch s {
	case SortBySeverity:
		return "severity"
	case SortByName:
		return "alertname"
	case SortByState:
		return "state"
	case SortByAge:
		return "age"
	}
	return "?"
}

// Options bundles the per-page constructor inputs.
type Options struct {
	Styles theme.Styles
	// Now injects the wall clock for the age column. nil falls
	// back to time.Now.
	Now func() time.Time
	// Scope labels the active tenant set in the body title — one
	// tenant name when a single backend is selected, "all" when
	// every configured tenant is selected, or comma-joined names
	// when a subset is selected. Empty hides the parenthesised
	// scope from the title.
	Scope string
	// Clients is the per-tenant write surface the page hands to
	// the silence form when the user presses `s`. Empty in tests
	// or read-only runs — `s` flashes a hint instead. Same shape
	// as the silences page.
	Clients map[string]silenceform.Client
	// Creator seeds the silence form's CreatedBy field; usually
	// $USER. Empty falls back to "a10r" in the form factory.
	Creator string
}

// alertEntry pairs an alert with the tenant tag the poller
// emitted it under so the table can surface a TENANT column when
// the active scope spans multiple backends.
type alertEntry struct {
	a      backend.Alert
	tenant string
}

// Page is the alerts list view. Implements app.Page.
type Page struct {
	styles theme.Styles
	now    func() time.Time
	scope  string

	// clients is the per-tenant write surface for `s`; see Options.
	clients map[string]silenceform.Client
	// creator seeds the silence form's CreatedBy field.
	creator string

	// byTenant stores the most recent snapshot per tenant. Each
	// poller emits a DataMsg keyed to its own Tenant; recompute
	// unions the snapshots before sorting / filtering.
	byTenant map[string][]backend.Alert

	view   []alertEntry // filtered + sorted view (recomputed on change)
	cursor int          // index into view

	// topRow is the index of the first visible row in p.view. The
	// renderer reconciles topRow with cursor on every frame so the
	// cursor stays inside the visible window — scrolls down when
	// the cursor walks past the bottom, up when it walks past the
	// top. Set lazily because the renderer is the only consumer
	// that knows how many rows fit in the body height.
	topRow int

	filter string // active substring filter
	// preFilter is the pre-prompt snapshot the page restores on
	// PromptCancelledMsg{Mode: PromptFilter}. Nil iff no filter
	// prompt is open. Invariant relies on the App forwarding
	// PromptOpenedMsg to the top page when `/` is pressed; if that
	// auto-forward ever changes, the snapshot stays stale and
	// cancel becomes a silent no-op.
	preFilter *string
	// focusFingerprint is the alert the cursor was on before the
	// last recompute. Tracking by Fingerprint (not index) keeps
	// the cursor on the same alert across poll-tick refreshes,
	// sort changes, and filter changes. Empty when no alert is
	// focused (cold start, empty view).
	focusFingerprint string

	// marks is the set of fingerprints the user has Space-toggled
	// for bulk operations (Ctrl+S bulk silence in #30). Tracking
	// by Fingerprint, like the cursor focus, so the marks survive
	// re-sorts and re-filters without sliding onto unrelated
	// alerts.
	marks map[string]struct{}

	sort        SortKey
	sortAsc     bool
	stateFilter string // "" = all, otherwise an AlertState value
}

// New constructs a Page from the supplied Options. Initial
// state is no alerts, no filter, sorted by severity descending.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Page{
		styles:   opts.Styles,
		now:      now,
		scope:    opts.Scope,
		clients:  opts.Clients,
		creator:  opts.Creator,
		byTenant: map[string][]backend.Alert{},
		sort:     SortBySeverity,
		sortAsc:  false,
		marks:    map[string]struct{}{},
	}
}

// SetScope updates the active tenant scope and rebuilds the
// view so the title's `[N]` count and the rendered rows both
// reflect the new selection. Mirror of the app.ScopeChangedMsg
// handler — exists for direct callers (the cmd-bar wiring,
// tests) that don't go through bubbletea's message bus.
func (p *Page) SetScope(s string) {
	p.scope = s
	p.recompute()
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "alerts" }

// Title implements app.Page — k9s-style
// "alerts(<scope>)[<count>]" with the scope being the active
// tenant set ("all" / "prod" / "prod,staging" / etc.) and the
// count being the filtered/total view size.
func (p *Page) Title() string {
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalAlerts()
	if p.filter != "" || p.stateFilter != "" {
		return fmt.Sprintf("alerts(%s)[%d/%d]", scope, len(p.view), total)
	}
	return fmt.Sprintf("alerts(%s)[%d]", scope, total)
}

// totalAlerts is the unfiltered alert count within the current
// scope. Used by Title (for the [N] suffix) and by the empty-
// state hint (which differentiates "no alerts polled yet" from
// "no alerts match the active filter"). Honours scope so a
// `<1>` quick-switch updates the title's [N] to that tenant's
// alert count rather than the cross-tenant total.
func (p *Page) totalAlerts() int {
	n := 0
	for tenant, alerts := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		n += len(alerts)
	}
	return n
}

// showTenantColumn reports whether the page should render a
// leading TENANT column. True when the active scope spans more
// than one backend's data — which is what k9s does in its
// namespace=all view.
func (p *Page) showTenantColumn() bool {
	return p.scope == scopeAll && len(p.byTenant) > 1
}

// scopeIncludes reports whether tenant should appear in the
// view given p.scope. "all" / empty includes everyone; anything
// else does an exact-match on the tenant tag (the poller
// emitted DataMsg with the configured backend Name).
func (p *Page) scopeIncludes(tenant string) bool {
	if p.scope == "" || p.scope == scopeAll {
		return true
	}
	return tenant == p.scope
}

// HeaderContent implements app.Page. Surfaces filter / state-
// filter / mark count when active so the user can see at a glance
// what's been applied or queued. Sort state is intentionally
// absent — the column header carries the ↑/↓ indicator and
// repeating it here is noise. Returns empty when nothing is
// active so the App skips the subtitle line entirely.
func (p *Page) HeaderContent() string {
	var parts []string
	if p.filter != "" {
		parts = append(parts, "filter:"+p.filter)
	}
	if p.stateFilter != "" {
		parts = append(parts, "state:"+p.stateFilter)
	}
	if n := len(p.marks); n > 0 {
		parts = append(parts, fmt.Sprintf("marked:%d", n))
	}
	return strings.Join(parts, " · ")
}

// Footer implements app.Page. Alerts list doesn't surface
// ambient state in the bottom border (the silences page does;
// keeping this empty here so the alerts frame stays unchanged).
func (*Page) Footer() string { return "" }

// Bindings implements app.Page. Returns the per-view bindings
// surfaced in the header's right-zone hint strip.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "Enter", Description: "detail", View: "alerts"},
		{Key: "Space", Description: "mark", View: "alerts"},
		{Key: "s", Description: "silence", View: "alerts", Dangerous: true},
		{Key: "/", Description: "filter", View: "alerts"},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.DataMsg:
		alerts, ok := m.Resource.([]backend.Alert)
		if !ok {
			return p, nil
		}
		p.byTenant[m.Tenant] = alerts
		p.recompute()
		return p, nil
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.handleFilterPrompt(m)
		return p, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		p.snapshotFocus()
		return p, nil
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash the new silence ID so the
		// user has confirmation. Same shape the silences page uses.
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. Esc on the form is a non-event.
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleFilterPrompt centralises the four filter-prompt lifecycle
// messages so Update stays under the cyclop budget. Each branch:
//
//   - Opened: snapshot the active filter and clear it so the user
//     types against the unfiltered list (live filter rebuilds it
//     keystroke-by-keystroke).
//   - Changed: apply the in-flight value live; preFilter stays so
//     Esc can still roll back regardless of what's been typed.
//   - Submitted: commit the typed value (possibly empty, meaning
//     "clear the filter"); drop the pre-prompt snapshot.
//   - Cancelled: restore the snapshot.
//
// Command-mode prompt messages slip through unchanged — the alerts
// page only owns filter mode.
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

// handleKey processes vim-motion and per-view keys. Returns the
// page (possibly mutated) plus a Cmd. Split across handleMotion /
// handleSort / handleAction to keep each handler under cyclop=15.
func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleMotion(m) {
		return p, nil
	}
	if p.handleSort(m) {
		return p, nil
	}
	return p.handleAction(m)
}

// handleMotion processes cursor-walk keys. Returns true when the
// key was a motion so the caller stops the keymap walk.
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

// handleSort processes sort-column shortcuts (h/l walk plus
// Shift+letter direct shortcuts). Returns true when the key was
// a sort change.
//
// Direction semantics: pressing the active column's shortcut
// again flips ASC/DESC; pressing a different column's shortcut
// resets to that column's default direction. h/l walk also
// resets to default for the new column. This matches the spreadsheet-
// style "click again to invert" UX users expect.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "h", "left":
		p.applySort(prevSort(p.sort))
	case "l", "right":
		p.applySort(nextSort(p.sort))
	case "shift+s", "S":
		p.applySort(SortBySeverity)
	case "shift+n", "N":
		p.applySort(SortByName)
	case "shift+t", "T":
		p.applySort(SortByState)
	case "shift+a", "A":
		// `Shift+A` sorts by age. keybindings.md uses Shift+R for
		// receivers (a column we don't ship in v0.1) — the Age
		// column gets Shift+A as the unambiguous shortcut so the
		// alphabet stays mnemonic.
		p.applySort(SortByAge)
	default:
		return false
	}
	return true
}

// applySort updates the sort key and direction. Same key twice
// flips ASC↔DESC; switching to a new key resets direction to
// that column's default. Calls recompute so the view reflects
// the change immediately.
func (p *Page) applySort(k SortKey) {
	if p.sort == k {
		p.sortAsc = !p.sortAsc
	} else {
		p.sort = k
		p.sortAsc = defaultAsc(k)
	}
	p.recompute()
}

// defaultAsc returns the direction the column reads naturally as
// when first activated. Severity defaults to descending so
// critical (the highest rank) shows first; everything else is
// ascending (alphabetical / oldest-first).
func defaultAsc(k SortKey) bool { return k != SortBySeverity }

// handleAction processes the page's per-view action keys
// (Enter drill, Space mark, state-filter cycle, silence).
// Returns the page plus optional Cmd. Unrecognised keys are
// no-ops at this layer; the App's dispatcher had its turn
// earlier.
func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "enter":
		cmd := p.drillToDetail()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "t":
		p.cycleStateFilter()
		p.recompute()
	case "s":
		cmd := p.openSilenceFormForCursor()
		return p, cmd
	}
	return p, nil
}

// openSilenceFormForCursor pushes the silence form prefilled with
// the cursor alert's labels as matchers. Configuration errors win
// over view-state errors: an empty Clients map flashes the same
// "no writeable backend" hint even on a cold-start empty view, so
// a misconfigured user sees the actionable message first. The
// silenceform.MatchersFromLabels helper drops the synthetic
// `__name__` label — silencing on it would silence every alert
// carrying that metric name.
func (p *Page) openSilenceFormForCursor() tea.Cmd {
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no alert under the cursor")
	}
	entry := p.view[p.cursor]
	client, ok := p.clients[entry.tenant]
	if !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	matchers := silenceform.MatchersFromLabels(entry.a.Labels)
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Client:   client,
			Styles:   styles,
			Now:      now,
			Creator:  creator,
			Matchers: matchers,
		})
	})
}

// hintNoWriteableBackend is the shared "configure a writeable
// backend" message every page flashes when `s` lands but no
// silenceform.Client is available. Pulled to a const so a wording
// change touches one site.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

// flashFn returns a Cmd that emits a flash with the given level
// and text. Mirrors the silences page's helper so the alerts
// handlers stay one-liners.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

// toggleMarkAtCursor flips the mark on the row under the cursor.
// No-op on an empty view. Empty fingerprints (alerts without a
// stable identifier) are silently skipped — there's no key to
// associate the mark with.
func (p *Page) toggleMarkAtCursor() {
	if p.cursor >= len(p.view) {
		return
	}
	fp := p.view[p.cursor].a.Fingerprint
	if fp == "" {
		return
	}
	if _, ok := p.marks[fp]; ok {
		delete(p.marks, fp)
		return
	}
	p.marks[fp] = struct{}{}
}

// drillToDetail returns a Cmd that pushes the alert-detail page
// for the row under the cursor. Empty view falls through to a
// soft Info flash so the user sees a reason for the no-op.
//
// Clients / Creator are threaded so the detail page's `s` push
// hits the same backend the alerts list `s` would. Same map by
// reference — pages share the wiring layer's authoritative copy.
func (p *Page) drillToDetail() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no alert under the cursor")
	}
	entry := p.view[p.cursor]
	styles := p.styles
	now := p.now
	clients := p.clients
	creator := p.creator
	return app.PushPage(func() app.Page {
		return alert.New(alert.Options{
			Alert:   entry.a,
			Tenant:  entry.tenant,
			Styles:  styles,
			Now:     now,
			Clients: clients,
			Creator: creator,
		})
	})
}

// nextSort returns the next sort key in cycle order. Wraps from
// SortByAge to SortBySeverity.
func nextSort(k SortKey) SortKey {
	if k == SortByAge {
		return SortBySeverity
	}
	return k + 1
}

// prevSort returns the previous sort key. Wraps from SortBySeverity
// to SortByAge.
func prevSort(k SortKey) SortKey {
	if k == SortBySeverity {
		return SortByAge
	}
	return k - 1
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

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.filter != "" || p.stateFilter != "" {
		return "no alerts match the active filter — Esc clears the prompt, `t` cycles state filters"
	}
	if p.totalAlerts() == 0 {
		return "no alerts (yet) — the poller will refresh on the next tick"
	}
	return "no alerts in view"
}

// renderHeader returns the column-title row with a sort marker
// on the active column. Titles are upper-cased and styled via
// theme.Table.Header (k9s-style yellow on base in catppuccin) so
// they stand apart from the data rows. A leading TENANT column
// appears when the active scope spans multiple backends.
func (p *Page) renderHeader(width int) string {
	titles := []SortKey{SortBySeverity, SortByName, SortByState, SortByAge}
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
	line := strings.Repeat(" ", rowPrefixCols) + p.padColumns(parts, width)
	// Foreground-only render so the header row keeps the body
	// background — the user wants it visually flush with the data
	// rows underneath, not a coloured stripe.
	return lipgloss.NewStyle().
		Foreground(p.styles.Table.Header.GetForeground()).
		Render(line)
}

// renderRows returns the visible window of data rows. The window
// is reconciled against the cursor on every frame so the cursor
// stays inside it: scrolling down when the cursor walks past the
// bottom, up when it walks past the top.
//
// The cursor row is wrapped in the theme's Table.Cursor style so
// it stands out k9s-style — the background fills the full width
// of the body, not just the visible characters, by padding the
// rendered string to width before the style wraps it.
func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.view))

	showTenant := p.showTenantColumn()
	var b strings.Builder
	for i := p.topRow; i < end; i++ {
		entry := p.view[i]
		a := entry.a
		ageLabel := header.FormatAge(p.now(), a.StartsAt)
		if ageLabel == "" {
			ageLabel = "—"
		}
		_, marked := p.marks[a.Fingerprint]
		mark := " "
		if marked {
			mark = "✓"
		}
		// Per-cell severity colour applies only to plain rows.
		// Cursor / marked / suppressed rows wrap the entire line in
		// a row-level style; nested ANSI inside that wrap is fragile
		// across terminals, and per Q1.2 the row-level style is
		// supposed to win — so skip the cell-level colour entirely
		// for those three cases.
		rowStyled := i == p.cursor || marked || a.State == backend.AlertStateSuppressed
		sevCell := severityOf(a)
		if !rowStyled {
			sevCell = severityStyle(a, p.styles).Render(sevCell)
		}
		row := make([]string, 0, 5)
		if showTenant {
			row = append(row, entry.tenant)
		}
		row = append(row,
			sevCell,
			a.Labels["alertname"],
			string(a.State),
			ageLabel,
		)
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		// Pad to the full width before styling. Precedence:
		// cursor > marked > dimmed. Cursor wraps the whole row in
		// fg+bg (the salient "you are here" signal); Marked and
		// Dimmed both change the foreground only so the row keeps
		// the body's default background — k9s "tinted text"
		// rather than two competing highlighted stripes stacked on
		// top of each other. Dimmed fires when the alert is
		// suppressed (silenced / inhibited / muted by a time
		// interval) and is neither cursor nor marked — same
		// treatment k9s gives "Completed" pods. Marked beats
		// dimmed on purpose: marked is an explicit user action,
		// suppression is ambient state.
		line := padRight(prefix+mark+" "+p.padColumns(row, width), width)
		switch {
		case i == p.cursor:
			line = p.styles.Table.Cursor.Render(line)
		case marked:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		case a.State == backend.AlertStateSuppressed:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Dimmed.GetForeground()).
				Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// reconcileScroll slides topRow so the cursor lands inside the
// [topRow, topRow+maxRows) window. Called from the renderer
// because maxRows is body-height-dependent and only known here.
func (p *Page) reconcileScroll(maxRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	// Clamp: never scroll past the last possible window.
	maxTop := max(len(p.view)-maxRows, 0)
	if p.topRow > maxTop {
		p.topRow = maxTop
	}
	if p.topRow < 0 {
		p.topRow = 0
	}
}

// rowPrefixCols is the space the rendered row reserves for its
// leading "[cursor] [mark] " prefix (▸ or space + mark glyph or
// space + separator). renderHeader prepends the same number of
// spaces so the column titles line up with the data columns.
const rowPrefixCols = 4

// padColumns lays out the row's columns at fixed widths with one
// flex column for the alertname. The leading TENANT column is
// optional — added when scope spans multiple backends and parts
// has 5 entries instead of 4. Crude but adequate for v0.1; a
// future commit can swap in lipgloss/table.Table if ergonomics
// become a complaint.
func (p *Page) padColumns(parts []string, width int) string {
	// severity 12 / name flex / state 14 / age 12, plus optional
	// leading tenant 16. The flex column absorbs the remainder.
	tenantCol := 0
	if p.showTenantColumn() {
		tenantCol = 16
	}
	const sevCol, stateCol, ageCol = 12, 14, 12
	flex := max(width-tenantCol-sevCol-stateCol-ageCol-rowPrefixCols, 10)

	cols := []int{}
	if tenantCol > 0 {
		cols = append(cols, tenantCol)
	}
	cols = append(cols, sevCol, flex, stateCol, ageCol)

	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padRight(v, cols[i]))
	}
	return b.String()
}

// padRight truncates / right-pads s to exactly w runes.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) >= w {
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// truncate cuts s to at most w columns.
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

// recompute rebuilds p.view by unioning every per-tenant
// snapshot, applying the active filters, sorting, then re-
// resolving the cursor by fingerprint so the focused alert
// stays under the cursor across poll refreshes / sort changes /
// filter changes. When the focused alert is gone (filtered out,
// expired) the cursor clamps to the last row; the new focus
// fingerprint is captured before returning so the next
// recompute keeps tracking what the user is looking at.
func (p *Page) recompute() {
	flat := make([]alertEntry, 0)
	for tenant, alerts := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, a := range alerts {
			flat = append(flat, alertEntry{a: a, tenant: tenant})
		}
	}
	p.view = filterEntries(flat, p.filter, p.stateFilter)
	sortEntries(p.view, p.sort, p.sortAsc)

	// Resolve cursor by fingerprint when we have one to follow.
	if p.focusFingerprint != "" {
		for i, e := range p.view {
			if e.a.Fingerprint == p.focusFingerprint {
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

// snapshotFocus captures the fingerprint of the row currently
// under the cursor so subsequent recomputes can re-resolve it.
// Empty view leaves focus empty.
func (p *Page) snapshotFocus() {
	if p.cursor < len(p.view) {
		p.focusFingerprint = p.view[p.cursor].a.Fingerprint
		return
	}
	p.focusFingerprint = ""
}

// cycleStateFilter walks "" → active → suppressed → unprocessed → ""
// per the `t` binding's intent (cycle through state filters).
func (p *Page) cycleStateFilter() {
	cycle := []string{"", string(backend.AlertStateActive), string(backend.AlertStateSuppressed), string(backend.AlertStateUnprocessed)}
	for i, v := range cycle {
		if v == p.stateFilter {
			p.stateFilter = cycle[(i+1)%len(cycle)]
			return
		}
	}
	p.stateFilter = ""
}

// filterEntries returns a new slice containing only entries
// whose Alert matches both the substring and state filters.
// Substring is matched case-insensitively against label and
// annotation values.
func filterEntries(in []alertEntry, substr, state string) []alertEntry {
	if substr == "" && state == "" {
		out := make([]alertEntry, len(in))
		copy(out, in)
		return out
	}
	needle := strings.ToLower(substr)
	out := make([]alertEntry, 0, len(in))
	for _, e := range in {
		if state != "" && string(e.a.State) != state {
			continue
		}
		if substr != "" && !alertMatchesSubstr(e.a, needle) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// alertMatchesSubstr reports whether the needle (already
// lower-cased by the caller) appears in any of a's label values
// or annotation values. Annotations cover summary / description so
// a "high cpu" filter hits an alert whose annotation contains
// "High CPU usage" even when the alertname itself is opaque.
func alertMatchesSubstr(a backend.Alert, needle string) bool {
	for _, v := range a.Labels {
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	for _, v := range a.Annotations {
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	return false
}

// sortEntries sorts in place by the given key.
func sortEntries(out []alertEntry, key SortKey, asc bool) {
	less := lessFor(key)
	sort.SliceStable(out, func(i, j int) bool {
		if asc {
			return less(out[i], out[j])
		}
		return less(out[j], out[i])
	})
}

// lessFor returns the ascending less-than for the given sort key.
func lessFor(key SortKey) func(a, b alertEntry) bool {
	switch key {
	case SortByName:
		return func(a, b alertEntry) bool { return a.a.Labels["alertname"] < b.a.Labels["alertname"] }
	case SortByState:
		return func(a, b alertEntry) bool { return a.a.State < b.a.State }
	case SortByAge:
		return func(a, b alertEntry) bool { return a.a.StartsAt.Before(b.a.StartsAt) }
	default: // SortBySeverity
		return func(a, b alertEntry) bool {
			return severityRank(a.a) < severityRank(b.a)
		}
	}
}

// severityRank assigns a numeric weight so descending sort puts
// critical first, then warning, info, then anything unknown.
// Higher rank = "more severe" so the ↓ arrow on the column header
// reads naturally as "most-severe-first".
func severityRank(a backend.Alert) int {
	switch strings.ToLower(a.Labels["severity"]) {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

// severityOf returns the printable severity label, falling back
// to "—" when no severity label is set.
func severityOf(a backend.Alert) string {
	if v, ok := a.Labels["severity"]; ok && v != "" {
		return v
	}
	return "—"
}

// severityStyle returns the lipgloss style for a's severity label so
// the renderer can foreground-tint the SEVERITY cell. Falls back to
// Severity.Unknown for missing / unrecognised values so every cell
// gets a consistent palette ref rather than a bare default.
func severityStyle(a backend.Alert, styles theme.Styles) lipgloss.Style {
	switch strings.ToLower(a.Labels["severity"]) {
	case "critical":
		return styles.Severity.Critical
	case "warning":
		return styles.Severity.Warning
	case "info":
		return styles.Severity.Info
	}
	return styles.Severity.Unknown
}

// SPDX-License-Identifier: Apache-2.0

// Package receivers renders the receivers list. Receivers carry
// only a Name on the AM side, so each row is mostly the drill-down:
// an Enter on a row pushes the alerts page filtered by `receiver=…`.
// In an all-tenant scope across a multi-backend fleet a leading
// TENANT column tags which backend each receiver came from —
// mirroring the alerts / silences / groups pages.
package receivers

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
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

// sortKeyName is the single sortable axis. Receivers carry only a
// name on the AM side, so the page exposes one column; the helper's
// degenerate len(cols)==1 path handles h/l as no-op while Shift+N
// flips ASC↔DESC for parity with the alerts / silences page sort
// idiom.
const sortKeyName = "name"

// receiverSortColumns returns the page's single sortable axis. The
// helper still applies the same "press the active column to flip
// direction" idiom — flipping ASC↔DESC is the only state change
// possible on a single-axis page.
//
// Tenant is the comparator's tiebreaker, not a sortable axis of its
// own: flatten walks byTenant in Go's random map order, so the same
// receiver name shared across two backends would otherwise jitter
// between renders. Sorting by name then tenant pins a stable row
// order the cursor can track.
func receiverSortColumns() []tablesort.Column[receiverEntry] {
	return []tablesort.Column[receiverEntry]{
		{
			Key: sortKeyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true,
			Less: func(a, b *receiverEntry) bool {
				if a.name != b.name {
					return a.name < b.name
				}
				return a.tenant < b.tenant
			},
		},
	}
}

// receiverEntry is a single receiver tagged with the backend it came
// from. Mirrors alertEntry / silenceEntry / groupEntry: the page
// flattens byTenant into these so a receiver name shared across
// tenants renders one row per tenant rather than a de-duplicated
// union.
type receiverEntry struct {
	name   string
	tenant string
}

// receiverTenantW is the fixed width of the leading TENANT column
// when shown. Matches the tenant floor on the alerts / silences
// pages so the column lines up across views.
const receiverTenantW = 16

// Options bundles the per-page constructor inputs. Mirrors the
// shape of the alerts / silences / groups pages so the wiring
// layer threads a uniform struct into every list page.
type Options struct {
	Styles *theme.Styles
	// Tenants is the canonical list of configured backend names so
	// a broken tenant still counts toward "is this a multi-tenant
	// fleet?". The page also uses it to gate incoming DataMsg /
	// BackendStatusMsg state mutations: a tenant name not in this
	// list never lands in byTenant / BackendHealth so a wire-layer
	// bug, hot-reload that didn't prune sources, or stray test
	// fixture cannot pollute the page with names that will never
	// poll or render. Empty disables the guard for tests / legacy
	// wiring that don't pin the list.
	Tenants []string
}

// Page is the receivers list view.
//
// Receivers are flat strings on the AM side; the page flattens the
// per-backend snapshots into one row per (tenant, receiver) so a
// name shared across backends stays distinguishable under the
// TENANT column rather than collapsing into a union. The drill-down
// keeps the behaviour the user expects (Enter on a row →
// DrillRequestMsg with the receiver name) since the receiver name is
// unique per backend in practice.
type Page struct {
	listpage.Base

	styles *theme.Styles

	// byTenant holds the most recent snapshot per backend, keyed
	// by the poll.DataMsg.Tenant tag.
	byTenant map[string][]string
	view     []receiverEntry // filtered + scoped + sorted subset

	// sorter owns the active sort state. Receivers expose a single
	// sortable axis (Name) so the helper's degenerate single-column
	// path handles h/l as no-op; Shift+N flips ASC↔DESC.
	sorter *tablesort.Sorter[receiverEntry]

	// focusName / focusTenant identify the row the cursor was on
	// before the most recent recompute — used to restore the cursor
	// onto the same receiver after a re-sort, scope change, or poll
	// refresh. Both are needed because a receiver name is unique
	// only within a tenant, so the cross-tenant view can hold two
	// rows sharing a name. Mirrors focusFingerprint / focusID /
	// focusKey on the alerts / silences / groups pages.
	focusName   string
	focusTenant string
}

// scopeAll is the canonical "every configured tenant" label.
const scopeAll = "all"

// New constructs an empty receivers page from the supplied Options.
func New(opts Options) *Page {
	p := &Page{
		Base: listpage.Base{
			Scope:         scopeAll,
			BackendHealth: map[string]listpage.BackendHealth{},
			Tenants:       opts.Tenants,
		},
		styles:   opts.Styles,
		byTenant: map[string][]string{},
		sorter:   tablesort.New(receiverSortColumns(), sortKeyName),
	}
	p.Recompute = p.recompute
	p.RowCount = func() int { return len(p.view) }
	p.SnapshotFocus = p.snapshotFocus
	return p
}

func (*Page) Init() tea.Cmd { return nil }

func (*Page) Close() tea.Cmd { return nil }

func (*Page) Crumb() string { return "receivers" }

// Title implements app.Page. Mirrors the alerts shape:
// `receivers(<scope>)[<count>]` or `receivers(<scope>)[F/T]`
// while a filter is active.
func (p *Page) Title() string {
	scope := p.Scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalReceivers()
	if p.Filter != "" {
		return fmt.Sprintf("receivers(%s)[%d/%d]", scope, len(p.view), total)
	}
	return fmt.Sprintf("receivers(%s)[%d]", scope, total)
}

// totalReceivers is the unfiltered per-tenant receiver count within
// the current scope. Used by Title (for the [N] suffix) and by the
// empty-state hint to tell "nothing polled yet" apart from "filter
// hides everything". Counts one per (tenant, receiver) — matching
// the per-tenant rows the table renders — rather than the
// de-duplicated union.
func (p *Page) totalReceivers() int {
	n := 0
	for tenant, names := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		n += len(names)
	}
	return n
}

// flatten builds the receiverEntry slice for in-scope tenants, one
// entry per (tenant, receiver). A receiver name shared across
// backends yields one row per tenant rather than collapsing into a
// single de-duplicated entry — the TENANT column disambiguates.
func (p *Page) flatten() []receiverEntry {
	flat := make([]receiverEntry, 0, p.totalReceivers())
	for tenant, names := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		for _, n := range names {
			flat = append(flat, receiverEntry{name: n, tenant: tenant})
		}
	}
	return flat
}

// HeaderContent implements app.Page. Surfaces the active filter
// (when any) so the user can see what's been applied without
// re-opening the prompt. Empty otherwise — count lives in Title.
func (p *Page) HeaderContent() string {
	if p.Filter != "" {
		return "filter:" + p.Filter
	}
	return ""
}

// Footer implements app.Page. Receivers list surfaces only the
// watch-mode marker — there is no per-tenant refresh countdown
// to render (unlike alerts / silences / groups) so the bottom
// border stays empty in the normal case.
func (p *Page) Footer() string {
	if p.Paused {
		return "WATCH OFF"
	}
	return ""
}

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache only replays "receivers" payloads into this
// page on push.
func (*Page) PollResources() []string { return []string{"receivers"} }

// Bindings implements app.Page. Sort shortcut comes from the
// tablesort helper; the helper's single-column setup emits exactly
// one Shift+N entry so the help overlay's RESOURCE column picks it
// up identically to the multi-axis pages.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("receivers")
	out := make([]action.Action, 0, 2+len(sortBindings))
	out = append(out, action.Action{Key: "Enter", Description: "drill", View: "receivers"})
	out = append(out, sortBindings...)
	out = append(out, action.Action{Key: "w", Description: "toggle watch", View: "receivers"})
	return out
}

func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		p.HandleBackendStatusMsg(m)
		return p, nil
	case poll.DataMsg:
		recs, ok := m.Resource.([]backend.Receiver)
		if !ok {
			return p, nil
		}
		// Same guard as BackendStatusMsg: refuse data from tenants
		// not in the configured list so byTenant doesn't hold
		// entries for names that will never be polled or rendered.
		if !p.KnownTenant(m.Tenant) {
			return p, nil
		}
		// Watch-mode: paused pages drop the snapshot so the table
		// does not move under the cursor mid-read. Receivers has
		// no manual `r` refresh, so there is no pausedRefresh
		// escape hatch — every incoming DataMsg is dropped while
		// paused. Press `w` again to resume.
		if p.Paused {
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
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.HandleFilterPrompt(m)
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// recompute rebuilds the filtered view from byTenant + p.Scope +
// p.Filter and clamps the cursor to the new range. The active
// sort direction is applied last so the visible order reflects
// the user's toggle. The /-prompt filter is auto-classified
// (substring / fuzzy / literal / regex) by footer.NewMatcher.
func (p *Page) recompute() {
	flat := p.flatten()
	matcher := footer.NewMatcher(p.Filter)
	if matcher.MatchAll() {
		p.view = flat
	} else {
		p.view = make([]receiverEntry, 0, len(flat))
		for _, e := range flat {
			if matcher.Match(strings.ToLower(e.name)) {
				p.view = append(p.view, e)
			}
		}
	}
	// Apply through the helper so all list pages use the same sort
	// machinery. The comparator's name-then-tenant order pins a
	// stable layout over flatten's random map-iteration order; the
	// DESC case reverses per the helper's flipped-arg comparator.
	p.sorter.Apply(p.view)
	// Resolve cursor by (name, tenant) when we have a focus to
	// follow so the user stays on the same receiver across re-sort /
	// scope / poll. Falls through to the clamp + re-snapshot path
	// when focus is empty or the focused row vanished.
	if p.focusName != "" {
		for i, e := range p.view {
			if e.name == p.focusName && e.tenant == p.focusTenant {
				p.SetIndex(i, len(p.view))
				return
			}
		}
	}
	p.Clamp(len(p.view))
	p.snapshotFocus()
}

// snapshotFocus captures the name of the row currently under the
// cursor so the next recompute can re-resolve it. Empty view
// leaves focus empty.
func (p *Page) snapshotFocus() {
	if p.Index() < 0 || p.Index() >= len(p.view) {
		p.focusName = ""
		p.focusTenant = ""
		return
	}
	e := p.view[p.Index()]
	p.focusName = e.name
	p.focusTenant = e.tenant
}

// handleSort processes sort-axis shortcuts. The tablesort helper's
// degenerate single-column path makes h/l a documented no-op (no
// other column to walk to); Shift+N flips ASC↔DESC via the same
// flip-on-repeat rule the multi-axis pages use. Returns true when
// the key was a sort interaction so the caller skips its other
// branches.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "h", "left", "l", "right":
		// Single-axis page: walk has nowhere to go. Eat the key so
		// the global "h/l = sort column" promise reads as "no
		// walking to do here" rather than falling through.
		return true
	}
	return cursor.HandleSort(
		m.String(),
		p.sorter,
		func() { p.focusName, p.focusTenant = "", "" },
		p.recompute,
	)
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleSort(m) {
		return p, nil
	}
	if changed, handled := p.MoveCursor(m.String(), len(p.view)); handled {
		if changed {
			p.snapshotFocus()
		}
		return p, nil
	}
	if m.String() == "enter" && p.Index() < len(p.view) {
		rec := p.view[p.Index()].name
		return p, func() tea.Msg { return DrillRequestMsg{Receiver: rec} }
	}
	if m.String() == "w" {
		p.toggleWatch()
		return p, nil
	}
	return p, nil
}

// toggleWatch flips paused state. Mirrors the alerts page's
// helper, minus the pausedRefresh handling — receivers has no
// manual `r` refresh so the one-shot escape hatch does not apply.
func (p *Page) toggleWatch() {
	p.Paused = !p.Paused
}

func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	band := p.RenderErrorBand(time.Now(), width, p.styles.Severity.Critical.GetForeground())
	bandLines := 0
	if band != "" {
		bandLines = 1
	}
	p.SetViewport(height-1-bandLines, len(p.view))
	if len(p.view) == 0 {
		msg := "no receivers (yet)"
		if p.totalReceivers() > 0 && p.Filter != "" {
			msg = "no receivers match the active filter — Esc clears the prompt"
		}
		// Render bg-less so the empty pane keeps the terminal
		// default background that the populated frame uses.
		body := msg
		if band != "" {
			body = band + "\n" + msg
		}
		return listpage.Pane(width, height, body)
	}
	maxRows := min(height-1-bandLines, len(p.view))
	end := min(p.TopRow()+maxRows, len(p.view))
	rows := make([]string, 0, end-p.TopRow()+2)
	if band != "" {
		rows = append(rows, band)
	}
	rows = append(rows, p.renderHeader())
	showTenant := p.ShowTenantColumn(len(p.byTenant))
	for i := p.TopRow(); i < end; i++ {
		e := p.view[i]
		prefix := "  "
		if i == p.Index() {
			prefix = "▸ "
		}
		var b strings.Builder
		b.WriteString(prefix)
		if showTenant {
			b.WriteString(format.PadRight(e.tenant, receiverTenantW))
		}
		b.WriteString(e.name)
		// Pad to width before applying the cursor style so the
		// background extends across the whole row k9s-style. The
		// assembled line is still plain text here, so PadRight's
		// overflow-truncation walks runes safely (no ANSI to split).
		row := format.PadRight(b.String(), width)
		if i == p.Index() {
			// k9s parity: cursor bg tracks the row's semantic
			// colour. Receiver rows have no severity / state, so
			// we use Severity.Info (k9s StdColor equivalent).
			rowColor := p.styles.Severity.Info.GetForeground()
			row = p.styles.Table.CursorOver(rowColor).Render(row)
		}
		rows = append(rows, row)
	}
	return listpage.Wrap(width, strings.Join(rows, "\n"))
}

// renderHeader emits the column-title strip with the active sort
// arrow. NAME is the sole sortable axis so it always carries the
// active-column foreground and the ASC↔DESC arrow; a leading TENANT
// column (display-only) is prepended when the all-tenant scope spans
// a multi-backend fleet, mirroring the alerts / silences / groups
// pages.
//
// fg-only renders (HeaderFg / HeaderActiveFg) so the header keeps
// the terminal default background — painted palette bg in the
// unstyled body frame creates a coloured stripe.
func (p *Page) renderHeader() string {
	label := strings.ToUpper("name")
	if arrow := p.sorter.ArrowFor(sortKeyName); arrow != "" {
		label = label + " " + arrow
	}
	var b strings.Builder
	b.WriteString("  ")
	if p.ShowTenantColumn(len(p.byTenant)) {
		b.WriteString(p.styles.Table.HeaderFg.Render(format.PadRight("TENANT", receiverTenantW)))
	}
	b.WriteString(p.styles.Table.HeaderActiveFg.Render(label))
	return b.String()
}

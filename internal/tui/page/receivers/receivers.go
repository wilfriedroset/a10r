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
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/format"
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
func receiverSortColumns() []tablesort.Column[string] {
	return []tablesort.Column[string]{
		{
			Key: sortKeyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true,
			Less: func(a, b *string) bool { return *a < *b },
		},
	}
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
	styles *theme.Styles

	// byTenant holds the most recent snapshot per backend, keyed
	// by the poll.DataMsg.Tenant tag.
	byTenant map[string][]string
	view     []string // filtered + scoped + de-duplicated subset
	cursor   int
	topRow   int // first visible row; reconciled against cursor on every render

	// bodyHeight is the row capacity snapshotted on the most recent
	// View. Ctrl+D / Ctrl+U step half this; Ctrl+F / Ctrl+B step
	// body-2 (vim's CTRL-F two-line overlap convention). Zero before
	// the first render — handlers fall back to 10 / 20 so a keystroke
	// that beats the initial WindowSizeMsg still moves a sane distance.
	bodyHeight int

	// filter is the active substring filter; preFilter is the
	// snapshot the page restores on PromptCancelledMsg per the
	// shared `/`-prompt contract (see alerts page for the full
	// lifecycle doc).
	filter    string
	preFilter *string

	// scope mirrors the active tenant scope ("all" / single name
	// / comma-joined subset). Updated by app.ScopeChangedMsg.
	scope string

	// sorter owns the active sort state. Receivers expose a single
	// sortable axis (Name) so the helper's degenerate single-column
	// path handles h/l as no-op; Shift+N flips ASC↔DESC.
	sorter *tablesort.Sorter[string]

	// focusName is the name of the row the cursor was on before
	// the most recent recompute — used to restore the cursor onto
	// the same receiver after a re-sort, scope change, or poll
	// refresh. Mirrors focusFingerprint / focusID / focusKey on
	// the alerts / silences / groups pages.
	focusName string

	// paused, when true, suppresses the byTenant/recompute branch
	// on incoming poll.DataMsg so the table stops updating under
	// the cursor mid-read. Toggled by `w` ("watch mode"). Mirrors
	// the alerts page — see internal/tui/page/alerts/alerts.go for
	// the design notes. Receivers has no manual `r` refresh so the
	// alerts-page pausedRefresh one-shot escape hatch does not
	// apply here; while paused, every DataMsg is dropped.
	paused bool

	// lastErrors holds the most recent per-tenant transport error
	// surfaced via poll.BackendStatusMsg.Detail. Empty entries are
	// pruned (a successful tick clears the row). Renderer collapses
	// the in-scope subset into a single one-line error band above
	// the table so the operator sees what's wrong without leaving
	// the page. Mirrors the alerts page's contract.
	lastErrors map[string]string
}

// scopeAll is the canonical "every configured tenant" label.
const scopeAll = "all"

// New constructs an empty receivers page.
func New(styles *theme.Styles) *Page {
	return &Page{
		styles:     styles,
		byTenant:   map[string][]string{},
		scope:      scopeAll,
		sorter:     tablesort.New(receiverSortColumns(), sortKeyName),
		lastErrors: map[string]string{},
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

// Footer implements app.Page. Receivers list surfaces only the
// watch-mode marker — there is no per-tenant refresh countdown
// to render (unlike alerts / silences / groups) so the bottom
// border stays empty in the normal case.
func (p *Page) Footer() string {
	if p.paused {
		return "WATCH OFF"
	}
	return ""
}

// ErrorBand returns the one-line message rendered above the
// table when at least one in-scope tenant is reporting a
// transport error. Empty when every in-scope tenant is healthy
// (or unpolled) so the renderer can short-circuit. Mirrors the
// alerts page — see internal/tui/page/alerts/alerts.go for the
// canonical doc.
func (p *Page) ErrorBand() string {
	type entry struct {
		tenant string
		detail string
	}
	var bad []entry
	for tenant, detail := range p.lastErrors {
		if detail == "" {
			continue
		}
		if !p.scopeIncludes(tenant) {
			continue
		}
		bad = append(bad, entry{tenant: tenant, detail: detail})
	}
	if len(bad) == 0 {
		return ""
	}
	// Sort by tenant for deterministic output across runs (map
	// iteration order is unspecified).
	sort.Slice(bad, func(i, j int) bool { return bad[i].tenant < bad[j].tenant })
	if len(bad) == 1 {
		// Single offender: tenant prefix only useful when scope
		// covers >1 tenant (avoids "prod: …" noise on a
		// single-tenant view).
		if p.scope == scopeAll || strings.Contains(p.scope, ",") {
			return bad[0].tenant + ": " + bad[0].detail
		}
		return bad[0].detail
	}
	return fmt.Sprintf("%d backends erroring; %s: %s", len(bad), bad[0].tenant, bad[0].detail)
}

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache only replays "receivers" payloads into this
// page on push.
func (*Page) PollResources() []string { return []string{"receivers"} }

// Bindings implements app.Page. Sort shortcut comes from the
// tablesort helper; the helper's single-column setup emits exactly
// one Shift+N entry so the help overlay's HOTKEYS column picks it
// up identically to the multi-axis pages.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("receivers")
	out := make([]action.Action, 0, 2+len(sortBindings))
	out = append(out, action.Action{Key: "Enter", Description: "drill", View: "receivers"})
	out = append(out, sortBindings...)
	out = append(out, action.Action{Key: "w", Description: "toggle watch (pause poll)", View: "receivers"})
	return out
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		// Track per-tenant transport errors for the error band.
		// A successful transition (Detail empty) clears the row;
		// failure transitions overwrite with the latest detail
		// the operator should see. Mirror of the alerts page's
		// handler.
		if m.Detail == "" {
			delete(p.lastErrors, m.Tenant)
		} else {
			p.lastErrors[m.Tenant] = m.Detail
		}
		return p, nil
	case poll.DataMsg:
		recs, ok := m.Resource.([]backend.Receiver)
		if !ok {
			return p, nil
		}
		// Watch-mode: paused pages drop the snapshot so the table
		// does not move under the cursor mid-read. Receivers has
		// no manual `r` refresh, so there is no pausedRefresh
		// escape hatch — every incoming DataMsg is dropped while
		// paused. Press `w` again to resume.
		if p.paused {
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
		p.snapshotFocus()
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
// p.filter and clamps the cursor to the new range. The active
// sort direction is applied last so the visible order reflects
// the user's toggle. The /-prompt filter is auto-classified
// (substring / fuzzy / literal / regex) by footer.NewMatcher.
func (p *Page) recompute() {
	scoped := p.unionScoped()
	matcher := footer.NewMatcher(p.filter)
	if matcher.MatchAll() {
		p.view = scoped
	} else {
		p.view = p.view[:0]
		for _, name := range scoped {
			if matcher.Match(strings.ToLower(name)) {
				p.view = append(p.view, name)
			}
		}
	}
	// Apply through the helper so all list pages use the same sort
	// machinery. unionScoped already emits names in ASC order, so
	// the ASC case is a no-op stable resort; the DESC case reverses
	// per the helper's flipped-arg comparator.
	p.sorter.Apply(p.view)
	// Resolve cursor by focusName when we have one to follow so
	// the user stays on the same receiver across re-sort / scope /
	// poll. Falls through to the clamp + re-snapshot path when
	// focus is empty or the focused name vanished.
	if p.focusName != "" {
		for i, name := range p.view {
			if name == p.focusName {
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

// snapshotFocus captures the name of the row currently under the
// cursor so the next recompute can re-resolve it. Empty view
// leaves focus empty.
func (p *Page) snapshotFocus() {
	if p.cursor < 0 || p.cursor >= len(p.view) {
		p.focusName = ""
		return
	}
	p.focusName = p.view[p.cursor]
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
		func() { p.focusName = "" },
		p.recompute,
	)
}

func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleSort(m) {
		return p, nil
	}
	// `g` alone is dead code — the dispatcher's chord buffer at
	// LayerTable consumes the first `g` waiting for the second. The
	// chord-completed `gg` arrives as app.GoToFirstRowMsg and is
	// handled in Update.
	if newCursor, handled := cursor.HandleMotion(
		m.String(),
		p.cursor,
		len(p.view),
		cursor.HalfPageStep(p.bodyHeight),
		cursor.FullPageStep(p.bodyHeight),
	); handled {
		p.cursor = newCursor
		p.snapshotFocus()
		return p, nil
	}
	if m.String() == "enter" && p.cursor < len(p.view) {
		rec := p.view[p.cursor]
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
	p.paused = !p.paused
}


// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	band := p.renderErrorBand(width)
	bandLines := 0
	if band != "" {
		bandLines = 1
	}
	p.bodyHeight = height - 1 - bandLines // header + optional error band; rest is row budget
	if len(p.view) == 0 {
		msg := "no receivers (yet)"
		if len(p.unionScoped()) > 0 && p.filter != "" {
			msg = "no receivers match the active filter"
		}
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		body := msg
		if band != "" {
			body = band + "\n" + msg
		}
		return lipgloss.NewStyle().Width(width).Height(height).Render(body)
	}
	maxRows := min(height-1-bandLines, len(p.view))
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, maxRows, len(p.view))
	end := min(p.topRow+maxRows, len(p.view))
	rows := make([]string, 0, end-p.topRow+2)
	if band != "" {
		rows = append(rows, band)
	}
	rows = append(rows, p.renderHeader(width))
	for i := p.topRow; i < end; i++ {
		text := p.view[i]
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		// Pad to width before applying the cursor style so the
		// background extends across the whole row k9s-style.
		row := format.PadRight(prefix+text, width)
		if i == p.cursor {
			// k9s parity: cursor bg tracks the row's semantic
			// colour. Receiver rows have no severity / state, so
			// we use Severity.Info (k9s StdColor equivalent).
			rowColor := p.styles.Severity.Info.GetForeground()
			row = p.styles.Table.CursorOver(rowColor).Render(row)
		}
		rows = append(rows, row)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

// renderErrorBand returns a one-line styled error message for the
// View to prepend, or "" when no in-scope tenant is reporting an
// error. Mirrors the alerts page's helper — fg-tinted with the
// severity-critical foreground (no painted background per the
// chrome-rendering memory) and clipped to width with SGRTruncate
// so a long upstream error doesn't break the layout.
func (p *Page) renderErrorBand(width int) string {
	msg := p.ErrorBand()
	if msg == "" {
		return ""
	}
	prefix := "! "
	full := prefix + msg
	if lipgloss.Width(full) > width {
		full = format.SGRTruncate(full, width)
	}
	style := lipgloss.NewStyle().Foreground(p.styles.Severity.Critical.GetForeground())
	return style.Render(full)
}

// renderHeader emits the column-title strip with the active sort
// arrow. Receivers carry a single sortable axis (Name) so the
// header always shows one label; the arrow flips ASC↔DESC on
// Shift+N. Active-column foreground (theme.Table.HeaderActive)
// applies because the sole column is by definition the active one
// — mirrors the multi-axis pages so the convention is uniform.
//
// fg-only render so the header keeps the terminal default
// background — painted palette bg in the unstyled body frame
// creates a coloured stripe.
func (p *Page) renderHeader(width int) string {
	label := strings.ToUpper("name")
	if arrow := p.sorter.ArrowFor(sortKeyName); arrow != "" {
		label = label + " " + arrow
	}
	body := format.PadRight("  "+label, width)
	return lipgloss.NewStyle().
		Foreground(p.styles.Table.HeaderActive.GetForeground()).
		Render(body)
}

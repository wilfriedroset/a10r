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
	"github.com/wilfriedroset/a10r/internal/tui/header"
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

// Page is the alerts list view. Implements app.Page.
type Page struct {
	styles theme.Styles
	now    func() time.Time

	all    []backend.Alert // most recent snapshot from the poller
	view   []backend.Alert // filtered + sorted view (recomputed on change)
	cursor int             // index into view

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

	sort        SortKey
	sortAsc     bool
	stateFilter string // "" = all, otherwise an AlertState value
}

// New constructs a Page with the given styles and an optional
// clock injector (nil falls back to time.Now). Initial state is
// no alerts, no filter, sorted by severity descending.
func New(styles theme.Styles, now func() time.Time) *Page {
	if now == nil {
		now = time.Now
	}
	return &Page{
		styles:  styles,
		now:     now,
		sort:    SortBySeverity,
		sortAsc: false,
	}
}

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "alerts" }

// HeaderContent implements app.Page. Surfaces the filter + sort
// state so the user always knows what shaping is active.
func (p *Page) HeaderContent() string {
	var parts []string
	if p.filter != "" {
		parts = append(parts, "filter:"+p.filter)
	}
	if p.stateFilter != "" {
		parts = append(parts, "state:"+p.stateFilter)
	}
	dir := "↓"
	if p.sortAsc {
		dir = "↑"
	}
	parts = append(parts, fmt.Sprintf("sort:%s %s", p.sort, dir))
	return strings.Join(parts, " · ")
}

// Bindings implements app.Page. Returns the per-view bindings
// surfaced in the header's right-zone hint strip.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "s", Description: "silence", View: "alerts", Dangerous: true},
		{Key: "/", Description: "filter", View: "alerts"},
		{Key: "?", Description: "help", View: ""},
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
		p.all = alerts
		p.recompute()
		return p, nil
	case footer.PromptOpenedMsg:
		if m.Mode == footer.PromptFilter {
			snap := p.filter
			p.preFilter = &snap
		}
		return p, nil
	case footer.PromptSubmittedMsg:
		if m.Mode == footer.PromptFilter {
			p.filter = m.Value
			p.preFilter = nil
			p.recompute()
		}
		return p, nil
	case footer.PromptCancelledMsg:
		if m.Mode == footer.PromptFilter && p.preFilter != nil {
			p.filter = *p.preFilter
			p.preFilter = nil
			p.recompute()
		}
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
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
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "h", "left":
		p.sort = prevSort(p.sort)
	case "l", "right":
		p.sort = nextSort(p.sort)
	case "shift+s", "S":
		p.sort = SortBySeverity
	case "shift+n", "N":
		p.sort = SortByName
	case "shift+t", "T":
		p.sort = SortByState
	case "shift+a", "A":
		// `Shift+A` sorts by age. keybindings.md uses Shift+R for
		// receivers (a column we don't ship in v0.1) — the Age
		// column gets Shift+A as the unambiguous shortcut so the
		// alphabet stays mnemonic.
		p.sort = SortByAge
	default:
		return false
	}
	p.recompute()
	return true
}

// handleAction processes the page's per-view action keys
// (state-filter cycle, silence). Returns the page plus optional
// Cmd. Unrecognised keys are no-ops at this layer; the App's
// dispatcher had its turn earlier.
func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "t":
		p.cycleStateFilter()
		p.recompute()
	case "s":
		// Silence form lands in #30. Today the binding flashes a
		// hint so users discover the affordance.
		return p, func() tea.Msg {
			return footer.FlashShowMsg{Level: footer.FlashWarn, Text: "silence form arrives in #30"}
		}
	}
	return p, nil
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
	rows := p.renderRows(width, height-2)
	footerLine := fmt.Sprintf("  %d/%d alerts · cursor=%d", len(p.view), len(p.all), p.cursor+1)
	body := strings.Join([]string{headerLine, rows, footerLine}, "\n")
	return lipgloss.NewStyle().Width(width).Render(body)
}

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.filter != "" || p.stateFilter != "" {
		return "no alerts match the active filter — Esc clears the prompt, `t` cycles state filters"
	}
	if len(p.all) == 0 {
		return "no alerts (yet) — the poller will refresh on the next tick"
	}
	return "no alerts in view"
}

// renderHeader returns the column-title row with a sort marker on
// the active column.
func (p *Page) renderHeader(width int) string {
	titles := []SortKey{SortBySeverity, SortByName, SortByState, SortByAge}
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

// renderRows returns the data rows, capped at maxRows visible.
// The cursor row is highlighted via the table.Cursor style.
func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 {
		return ""
	}
	var b strings.Builder
	for i, a := range p.view {
		if i >= maxRows {
			break
		}
		ageLabel := header.FormatAge(p.now(), a.StartsAt)
		if ageLabel == "" {
			ageLabel = "—"
		}
		row := []string{
			severityOf(a),
			a.Labels["alertname"],
			string(a.State),
			ageLabel,
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

// padColumns lays out the four columns at fixed proportions of
// the available width. Crude but adequate for v0.1 — a future
// commit can swap in lipgloss/table.Table if column ergonomics
// become a complaint.
func (p *Page) padColumns(parts []string, width int) string {
	cols := []int{12, 0, 14, 12} // severity, name (flex), state, age
	cols[1] = max(width-cols[0]-cols[2]-cols[3]-2, 10)
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

// recompute rebuilds p.view from p.all by applying the active
// filters and sort, then re-resolves the cursor by fingerprint so
// the focused alert stays under the cursor across poll refreshes,
// sort changes, and filter changes. When the focused alert is
// gone (filtered out, expired) the cursor clamps to the last row;
// the new focus fingerprint is captured before returning so the
// next recompute keeps tracking what the user is looking at.
func (p *Page) recompute() {
	p.view = filterAlerts(p.all, p.filter, p.stateFilter)
	sortAlerts(p.view, p.sort, p.sortAsc)

	// Resolve cursor by fingerprint when we have one to follow.
	if p.focusFingerprint != "" {
		for i, a := range p.view {
			if a.Fingerprint == p.focusFingerprint {
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
		p.focusFingerprint = p.view[p.cursor].Fingerprint
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

// filterAlerts returns a new slice containing only alerts that
// match both the substring and state filters. Substring is matched
// case-insensitively against alertname + every label value.
func filterAlerts(in []backend.Alert, substr, state string) []backend.Alert {
	if substr == "" && state == "" {
		out := make([]backend.Alert, len(in))
		copy(out, in)
		return out
	}
	needle := strings.ToLower(substr)
	out := make([]backend.Alert, 0, len(in))
	for _, a := range in {
		if state != "" && string(a.State) != state {
			continue
		}
		if substr != "" && !alertMatchesSubstr(a, needle) {
			continue
		}
		out = append(out, a)
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

// sortAlerts sorts in place by the given key.
func sortAlerts(out []backend.Alert, key SortKey, asc bool) {
	less := lessFor(key)
	sort.SliceStable(out, func(i, j int) bool {
		if asc {
			return less(out[i], out[j])
		}
		return less(out[j], out[i])
	})
}

// lessFor returns the ascending less-than for the given sort key.
func lessFor(key SortKey) func(a, b backend.Alert) bool {
	switch key {
	case SortByName:
		return func(a, b backend.Alert) bool { return a.Labels["alertname"] < b.Labels["alertname"] }
	case SortByState:
		return func(a, b backend.Alert) bool { return a.State < b.State }
	case SortByAge:
		return func(a, b backend.Alert) bool { return a.StartsAt.Before(b.StartsAt) }
	default: // SortBySeverity
		return func(a, b backend.Alert) bool {
			return severityRank(a) < severityRank(b)
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

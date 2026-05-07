// SPDX-License-Identifier: Apache-2.0

// Package alerts renders the alerts list page — the home view of
// the TUI per A1 / k9s-look-and-feel.md §3. v0.1 ships a minimal
// table:
//
//   - Vim motions (j/k/g/G/Ctrl+D/Ctrl+U/Ctrl+F/Ctrl+B) plus arrow keys.
//   - Substring filter via the `/` prompt (App routes
//     PromptSubmittedMsg{PromptFilter} to the page).
//   - Severity / alertname / instance / age columns.
//   - Per E2 sort cycling by `Shift+S` (severity), `Shift+N`
//     (alertname), `Shift+T` (state), `Shift+R` (receiver). `h`/`l`
//     walk between sort columns.
//   - `s` follows the k9s same-key-different-N rule: with no marks
//     it silences the cursor row via the per-row silence form;
//     with one or more marks it fans out a bulk silence — the
//     form opens once, the page substitutes per-target matchers
//     and dispatches CreateSilence per marked alert. Read-only
//     mode hides the binding via the action registry.
//
// Polling lives in the wiring layer (cmd/tui.go in #39): a poll
// loop emits DataMsg{Resource: []backend.Alert} that this page
// consumes via Update.
package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/alert"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Sort column keys. Stable identifiers passed to the tablesort
// helper — used as both the canonical Key (for ArrowFor / IsActive
// lookups) and the lower-cased description text in the help
// overlay (the helper derives "sort by <title>" from each Column's
// Title, which lower-cases to these strings). Order matches the
// cycle order for h/l walk per E2.
const (
	sortKeySeverity = "severity"
	sortKeyName     = "alertname"
	sortKeyState    = "state"
	sortKeyAge      = "age"
)

// scopeAll is the canonical label for the "every configured
// tenant" scope. Used by Title, scopeIncludes, and the
// `<0>` quick-switch payload — pinning it as a constant keeps
// the wiring layer and the page in lockstep.
const scopeAll = "all"

// alertSortColumns returns the page's sortable column set. Severity
// defaults DESC (critical first) — every other column reads naturally
// ascending. Comparators mirror the prior lessFor table verbatim.
func alertSortColumns() []tablesort.Column[alertEntry] {
	return []tablesort.Column[alertEntry]{
		{
			Key: sortKeySeverity, Title: "SEVERITY", Hotkey: 'S', DefaultAsc: false,
			Less: func(a, b alertEntry) bool {
				return backend.SeverityRank(a.a) < backend.SeverityRank(b.a)
			},
		},
		{
			Key: sortKeyName, Title: "ALERTNAME", Hotkey: 'N', DefaultAsc: true,
			Less: func(a, b alertEntry) bool {
				return a.a.Labels["alertname"] < b.a.Labels["alertname"]
			},
		},
		{
			Key: sortKeyState, Title: "STATE", Hotkey: 'T', DefaultAsc: true,
			Less: func(a, b alertEntry) bool { return a.a.State < b.a.State },
		},
		{
			Key: sortKeyAge, Title: "AGE", Hotkey: 'A', DefaultAsc: true,
			Less: func(a, b alertEntry) bool { return a.a.StartsAt.Before(b.a.StartsAt) },
		},
	}
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
	// TimeFormat seeds the page's time-format mode at construction
	// so a page pushed *after* the user toggled `t` doesn't open
	// in relative while the rest of the app reads absolute. Zero
	// value (TimeFormatRelative) is the pre-toggle default.
	TimeFormat app.TimeFormat
	// BulkConcurrency caps the per-tenant worker pool for the
	// bulk-silence fanout (one CreateSilence per marked alert).
	// Zero resolves to config.DefaultBulkConcurrency at construction
	// time so callers can pass the unmaterialised
	// `defaults.bulk_concurrency` directly.
	BulkConcurrency int
	// Logger receives per-failure detail (`backend`, `tenant`,
	// `alert_fingerprint`, `err`) at error level when the bulk
	// fanout surfaces individual CreateSilence failures. Nil
	// suppresses logging.
	Logger *slog.Logger
}

// bulkSilenceTarget is one resolved entry of a pending bulk-
// silence round. Tenant routes the CreateSilence call; Fingerprint
// is the alert identifier the page uses for the unmark step;
// Matchers is the per-alert label set the form's metadata is
// stamped onto. Captured at confirm-open time so a poll-tick
// reordering or filter change between confirm and Yes can't
// route a CreateSilence to a different alert on a different
// backend.
type bulkSilenceTarget struct {
	Tenant      string
	Fingerprint string
	Matchers    []backend.Matcher
}

// pendingBulkSilence captures the in-flight state between the
// confirm modal (or bulk-form push) and its result. Empty between
// rounds. Targets is the resolved list of {tenant, fingerprint,
// matchers}; tenants is a stable alphabetical list of distinct
// tenant names for the confirm question and the form banner.
type pendingBulkSilence struct {
	targets []bulkSilenceTarget
	tenants []string
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

	// bodyHeight is the table-row capacity snapshotted on the most
	// recent View — the App passes the bordered-body height; the
	// renderer subtracts the column-header line. Ctrl+D / Ctrl+U
	// step half this; Ctrl+F / Ctrl+B step body-2 (vim's CTRL-F
	// "two-line overlap" convention). Zero before the first render
	// — handlers fall back to 10 / 20 so a keystroke that beats the
	// initial WindowSizeMsg still moves a sane distance.
	bodyHeight int

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
	// for bulk operations. Tracking by Fingerprint, like the cursor
	// focus, so the marks survive re-sorts and re-filters without
	// sliding onto unrelated alerts. `s` with marks fans out one
	// CreateSilence per marked alert; failed targets keep their
	// marks so the next `s` retries only the unfinished work.
	marks map[string]struct{}

	// pendingBulkSilence captures the resolved bulk-silence targets
	// between an opened confirm modal (N≥2 marks) and its
	// ConfirmResultMsg, or between an opened bulk form (any N≥1)
	// and its BulkSubmittedMsg. Cleared after consumption.
	pendingBulkSilence pendingBulkSilence

	// bulkConcurrency caps the per-tenant worker pool for the
	// bulk-silence fanout. Tenants always run in parallel; this
	// knob limits the inner pool size per tenant.
	bulkConcurrency int
	// logger is the structured logger used for per-failure detail
	// in the bulk fanout. Nil suppresses logging.
	logger *slog.Logger
	// cancelBulk cancels the in-flight bulk-silence fanout when
	// set. Populated when fanout starts; the dispatch Cmd defers
	// its own cancel() so a stale done arriving after a newer
	// round started cannot abort the newer round (mirrors the
	// silences page's contract).
	cancelBulk context.CancelFunc

	// sorter owns the active sort column + direction. Comparators
	// and column metadata come from alertSortColumns; the helper
	// applies the cycle / flip / walk convention.
	sorter      *tablesort.Sorter[alertEntry]
	stateFilter string // "" = all, otherwise an AlertState value

	// timeFormat mirrors the app-global toggle. Defaults to
	// relative; flipped by app.TimeFormatChangedMsg so every list
	// page agrees on absolute vs. relative timestamps.
	timeFormat app.TimeFormat

	// polledTenants is the set of tenants that have produced at
	// least one DataMsg in this page's lifetime. Mirrors the
	// silences page's pattern so the title's "loading…" affordance
	// reads truthfully even in a multi-backend setup with a
	// scope-narrowed view: a fast out-of-scope tenant returning
	// [] doesn't flip the page out of loading state before the
	// in-scope tenant has answered.
	polledTenants map[string]struct{}
	// nextRefresh is the per-tenant DataMsg.NextAt timestamp.
	// Footer collapses it into "next refresh Ns" by picking the
	// soonest entry across in-scope tenants.
	nextRefresh map[string]time.Time
	// refreshing is true between an `r` press and the next in-scope
	// poll.DataMsg arrival so the renderer keeps the spinner up
	// while the caller's nudge is in flight.
	refreshing bool
	// spinner is the cold-start / refresh-in-flight indicator
	// (bubbles `Points`). Stopped (Tick chain broken) outside of
	// those two windows; see the spinner.TickMsg branch in Update.
	spinner spinner.Model
}

// New constructs a Page from the supplied Options. Initial
// state is no alerts, no filter, sorted by severity descending.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	sp := spinner.New(
		spinner.WithSpinner(spinner.Points),
		spinner.WithStyle(opts.Styles.Header.Accent),
	)
	concurrency := opts.BulkConcurrency
	if concurrency <= 0 {
		concurrency = config.DefaultBulkConcurrency
	}
	return &Page{
		styles:          opts.Styles,
		now:             now,
		scope:           opts.Scope,
		clients:         opts.Clients,
		creator:         opts.Creator,
		timeFormat:      opts.TimeFormat,
		byTenant:        map[string][]backend.Alert{},
		sorter:          tablesort.New(alertSortColumns(), sortKeySeverity),
		marks:           map[string]struct{}{},
		polledTenants:   map[string]struct{}{},
		nextRefresh:     map[string]time.Time{},
		spinner:         sp,
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
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

// Init implements app.Page. Kicks the spinner so the cold-start
// "loading" affordance animates while the first poll tick lands.
// The Tick chain is broken in Update once the page has any
// in-scope DataMsg (and re-armed on each manual `r` refresh) so
// the spinner stops costing per-frame redraws when there's
// nothing to wait for.
func (p *Page) Init() tea.Cmd { return p.spinner.Tick }

// Close implements app.Page. Cancels any in-flight bulk-silence
// fanout so a page pop while workers are mid-air aborts not-yet-
// started work via the worker channel select. In-flight HTTP
// requests are allowed to finish — CreateSilence is non-idempotent,
// so cancelling mid-flight risks a half-created silence; finishing
// the request and letting the user see the success / failure on
// the next poll is the safer trade-off.
func (p *Page) Close() tea.Cmd {
	if p.cancelBulk != nil {
		p.cancelBulk()
		p.cancelBulk = nil
	}
	return nil
}

// Crumb implements app.Page.
func (*Page) Crumb() string { return "alerts" }

// Title implements app.Page — k9s-style
// "alerts(<scope>)[<count>]" with the scope being the active
// tenant set ("all" / "prod" / "prod,staging" / etc.) and the
// count being the filtered/total view size. While the page is
// in a loading window — cold start (no in-scope DataMsg yet) or
// a manual `r` refresh in flight — the title flips to the
// spinner-led "loading alerts…" so the border itself reads as
// the loading affordance, k9s-style. Mirror of the silences
// page's pattern.
func (p *Page) Title() string {
	if !p.polled() || p.refreshing {
		return p.spinner.View() + " loading alerts…"
	}
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
// view given p.scope. "all" / empty includes everyone;
// otherwise the scope is parsed as a comma-joined list (so a
// Ctrl+T multi-select like "prod,staging" lights up both
// backends). Mirror of the silences-page predicate so the two
// list pages agree on scope shape.
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

// HeaderContent implements app.Page. Surfaces filter / state-
// filter / mark count when active so the user can see at a
// glance what's been applied or queued. Sort state is
// intentionally absent — the column header carries the ↑/↓
// indicator and repeating it here is noise. Time-format is
// intentionally absent too: the toggle's flash-on-press is the
// affordance signal, and the visible cell content (relative vs
// absolute) is self-evident — adding a subtitle here would steal
// a body row of real estate. Returns empty when nothing is
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

// Footer implements app.Page. Renders the next-refresh deadline
// — or "refreshing…" while a manual `r` is in flight — into the
// bordered body's bottom edge, k9s-style symmetry with the title
// in the top edge. Same shape as the silences page so the two
// list pages frame identically. Empty pre-poll so the cold-start
// frame stays quiet (the spinner already says "loading").
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

// soonestNextRefresh returns the earliest DataMsg.NextAt across
// in-scope tenants. Zero when no in-scope tenant has published a
// NextAt — the wiring layer's poll.DataMsg is the single source.
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
// renders as "due" so a slow tick reads honestly without flashing
// a negative duration. Mirrors the silences page's helper.
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

// polled reports whether at least one in-scope tenant has
// produced a DataMsg. Read by Title / Footer / emptyState to
// pick between the loading affordance and the populated frame.
// Scope-aware so a fast out-of-scope tenant in a multi-backend
// setup doesn't flip the page out of loading state before the
// in-scope tenant has answered.
func (p *Page) polled() bool {
	for tenant := range p.polledTenants {
		if p.scopeIncludes(tenant) {
			return true
		}
	}
	return false
}

// spinnerActive reports whether the spinner should continue to
// animate. Two windows: cold start and refresh-in-flight.
// Outside those, the page draws static "next refresh" timing.
func (p *Page) spinnerActive() bool { return !p.polled() || p.refreshing }

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache only replays "alerts" payloads into this page
// on push.
func (*Page) PollResources() []string { return []string{"alerts"} }

// Bindings implements app.Page. Returns the per-view bindings
// surfaced in the header's right-zone hint strip. Sort shortcuts
// come from the tablesort helper so every list page surfaces the
// same convention without each page hand-rolling the strings;
// h/l column walk lives on every table view via TableMotions and
// isn't repeated here.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("alerts")
	out := make([]action.Action, 0, 6+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "detail", View: "alerts"},
		action.Action{Key: "Space", Description: "mark", View: "alerts"},
		action.Action{Key: "s", Description: "silence", View: "alerts", Dangerous: true},
		action.Action{Key: "/", Description: "filter", View: "alerts"},
		action.Action{Key: "Shift+F", Description: "state filter", View: "alerts"},
	)
	out = append(out, sortBindings...)
	// `r` is a global binding too; surface it on the alerts hint
	// strip so the affordance reads at a glance alongside the
	// page-specific verbs. Same shape as silences.
	out = append(out, action.Action{Key: "r", Description: "refresh", View: "alerts"})
	return out
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.handleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.DataMsg:
		alerts, ok := m.Resource.([]backend.Alert)
		if !ok {
			return p, nil
		}
		p.byTenant[m.Tenant] = alerts
		// Capture poll metadata so Footer / Title can render without
		// a parallel ticker. Zero-valued NextAt (legacy / test
		// DataMsgs) leaves the prior entry intact.
		if !m.NextAt.IsZero() {
			p.nextRefresh[m.Tenant] = m.NextAt
		}
		p.polledTenants[m.Tenant] = struct{}{}
		// Only clear refreshing once an in-scope tenant has answered;
		// an out-of-scope reply during a manual `r` window would
		// otherwise drop the spinner before the user has actually
		// seen fresh data for the scope they're looking at.
		if p.scopeIncludes(m.Tenant) {
			p.refreshing = false
		}
		p.recompute()
		return p, nil
	case spinner.TickMsg:
		// Drop ticks outside the cold-start / refresh-in-flight
		// windows to break the self-perpetuating Tick chain when
		// nothing is loading.
		if !p.spinnerActive() {
			return p, nil
		}
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(m)
		return p, cmd
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.handleFilterPrompt(m)
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash the new silence ID so the
		// user has confirmation. Same shape the silences page uses.
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. Esc on the form is a non-event.
		// If a pending bulk round was waiting for the form, drop it
		// so a subsequent `s` doesn't reuse a stale target list.
		p.pendingBulkSilence = pendingBulkSilence{}
		return p, nil
	case silenceform.BulkSubmittedMsg:
		cmd := p.handleBulkSilenceSubmit(m)
		return p, cmd
	case modal.ConfirmResultMsg:
		cmd := p.handleBulkSilenceConfirm(m)
		return p, cmd
	case bulkSilenceDoneMsg:
		cmd := p.handleBulkSilenceDone(m)
		return p, cmd
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleSidebandMsg consumes the app-level sideband messages
// (scope change, time-format toggle, gg-chord first-row, Ctrl+\
// clear marks) so Update's main switch stays under the cyclop
// budget. Returns handled=true when the message was claimed and
// the caller should short-circuit the rest of Update.
func (p *Page) handleSidebandMsg(msg tea.Msg) (handled bool, cmd tea.Cmd) {
	switch m := msg.(type) {
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return true, nil
	case app.TimeFormatChangedMsg:
		p.timeFormat = m.Format
		return true, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		p.snapshotFocus()
		return true, nil
	case app.ClearMarksMsg:
		return true, p.handleClearMarks()
	}
	return false, nil
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
		p.cursor = min(p.cursor+p.halfPageStep(), max(len(p.view)-1, 0))
		p.snapshotFocus()
	case "ctrl+u":
		p.cursor = max(p.cursor-p.halfPageStep(), 0)
		p.snapshotFocus()
	case "ctrl+f":
		p.cursor = min(p.cursor+p.fullPageStep(), max(len(p.view)-1, 0))
		p.snapshotFocus()
	case "ctrl+b":
		p.cursor = max(p.cursor-p.fullPageStep(), 0)
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
	if !p.sorter.HandleKey(m.String()) {
		return false
	}
	// User-initiated re-sort is k9s-positional: the cursor stays at
	// the same index; whatever alert lands under it becomes the new
	// focus. Clearing focusFingerprint here bypasses the find-by-
	// fingerprint branch in recompute so the cursor is index-stable
	// for this one call. snapshotFocus then re-captures the new
	// alert so subsequent poll / scope / filter recomputes still
	// follow it (content-stable on data churn).
	p.focusFingerprint = ""
	p.recompute()
	return true
}

// handleAction processes the page's per-view action keys
// (Enter drill, Space mark, state-filter cycle, silence).
// Returns the page plus optional Cmd. Unrecognised keys are
// no-ops at this layer; the App's dispatcher had its turn
// earlier.
//
// State-filter cycling is bound to Shift+F (not `t`) since `t`
// is the app-global time-format toggle as of #9 — the
// dispatcher's global `t` consumes the key before the page sees
// it, so a local `t` handler here would be dead code. bubbletea
// v2's KeyPressMsg.String() emits the textual form ("F") for
// shift-modified letters — never "shift+f" — so a single `case
// "F"` is sufficient.
func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "enter":
		cmd := p.drillToDetail()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "F":
		p.cycleStateFilter()
		p.recompute()
	case "s":
		cmd := p.openSilenceForS()
		return p, cmd
	case "r":
		cmd := p.requestRefresh()
		return p, cmd
	}
	return p, nil
}

// requestRefresh emits a RefreshRequestedMsg so the wiring layer
// pokes the alerts pollers, flips the page into refreshing
// state, and (re)kicks the spinner Tick chain. Mirror of the
// silences page's helper.
func (p *Page) requestRefresh() tea.Cmd {
	p.refreshing = true
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "alerts", Scope: scope}
	}
	return tea.Batch(emit, p.spinner.Tick)
}

// openSilenceForS is the entry point for the `s` key. k9s-style:
// no marks → cursor row, single-form gate (existing wording);
// 1 mark → push the bulk form directly (the form is the gate at
// N=1 — a separate confirm would be redundant); ≥2 marks → confirm
// modal first, then bulk form. Mirror of the silences page's
// openExpireConfirmUnified shape.
func (p *Page) openSilenceForS() tea.Cmd {
	if len(p.marks) == 0 {
		return p.openSilenceFormForCursor()
	}
	return p.openBulkSilence()
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

// openBulkSilence resolves the marked alerts into bulkSilenceTargets
// (matchers minus `__name__`, paired with each alert's tenant) and
// either pushes the bulk form directly (N=1) or opens a confirm
// modal first (N≥2). Marks that no longer correspond to any in-
// scope alert (e.g. the alert resolved between mark and silence)
// are dropped silently. Empty Clients flashes the standard hint;
// no marks left after resolution drops to a soft Info flash.
func (p *Page) openBulkSilence() tea.Cmd {
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	targets, tenants := p.resolveBulkSilenceTargets()
	if len(targets) == 0 {
		return flashFn(footer.FlashInfo, "no marked alerts remain")
	}
	p.pendingBulkSilence = pendingBulkSilence{targets: targets, tenants: tenants}
	if len(targets) == 1 {
		return p.pushBulkSilenceForm()
	}
	question := fmt.Sprintf("silence %d alerts? (tenant %s)", len(targets), formatTenantBreakdownAlerts(targets))
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultYes)
	})
}

// resolveBulkSilenceTargets walks p.byTenant (not p.view) so a
// marked alert hidden by an active filter still ends up in the
// queue — the user marked it deliberately, an unrelated UI state
// shouldn't silently drop it. Targets are sorted by (tenant,
// fingerprint) so the confirm wording and the fanout order are
// stable across runs / tests. Returns the resolved list plus a
// stable alphabetical list of distinct tenant names for the
// confirm question + form banner.
func (p *Page) resolveBulkSilenceTargets() (targets []bulkSilenceTarget, tenants []string) {
	targets = make([]bulkSilenceTarget, 0, len(p.marks))
	tenantSet := map[string]struct{}{}
	for tenant, alerts := range p.byTenant {
		if _, ok := p.clients[tenant]; !ok {
			continue
		}
		for _, a := range alerts {
			if _, marked := p.marks[a.Fingerprint]; !marked {
				continue
			}
			targets = append(targets, bulkSilenceTarget{
				Tenant:      tenant,
				Fingerprint: a.Fingerprint,
				Matchers:    silenceform.MatchersFromLabels(a.Labels),
			})
			tenantSet[tenant] = struct{}{}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Tenant != targets[j].Tenant {
			return targets[i].Tenant < targets[j].Tenant
		}
		return targets[i].Fingerprint < targets[j].Fingerprint
	})
	tenants = make([]string, 0, len(tenantSet))
	for t := range tenantSet {
		tenants = append(tenants, t)
	}
	sort.Strings(tenants)
	return targets, tenants
}

// formatTenantBreakdownAlerts mirrors the silences page's
// formatTenantBreakdown shape but counts targets-per-tenant on a
// []bulkSilenceTarget rather than []pendingExpireID. Single-tenant
// returns the bare name; multi-tenant returns "name=count" pairs
// sorted alphabetically and joined with ", ".
func formatTenantBreakdownAlerts(targets []bulkSilenceTarget) string {
	counts := map[string]int{}
	tenants := []string{}
	for _, t := range targets {
		if _, seen := counts[t.Tenant]; !seen {
			tenants = append(tenants, t.Tenant)
		}
		counts[t.Tenant]++
	}
	sort.Strings(tenants)
	if len(tenants) == 1 {
		return tenants[0]
	}
	parts := make([]string, len(tenants))
	for i, t := range tenants {
		parts[i] = fmt.Sprintf("%s=%d", t, counts[t])
	}
	return strings.Join(parts, ", ")
}

// pushBulkSilenceForm pushes the silence form in bulk mode with
// a banner spelling out the per-target fanout shape. Uses the
// pending state populated by openBulkSilence — caller must have
// validated client availability already. Picks the first tenant's
// client as the form's Client field for symmetry with the single-
// form path; in bulk mode the form never calls Client, so the
// choice is cosmetic, but a non-nil value avoids a no-op fail
// branch in the form.
//
// The 1-mark path renders this banner ("applies to 1 alert
// (tenant prod)") rather than the cursor row's matchers buffer
// the no-marks path uses. Two single-target paths render and
// confirm differently on purpose: the bulk path can't show a
// single backend ID up front (the silence is created post-submit)
// and the form's banner is the user's gate at N=1. Don't try to
// "unify" them — the divergence is per-design.
func (p *Page) pushBulkSilenceForm() tea.Cmd {
	pending := p.pendingBulkSilence
	if len(pending.targets) == 0 {
		return flashFn(footer.FlashInfo, "no marked alerts remain")
	}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	banner := bulkSilenceBanner(pending.targets, pending.tenants)
	// Pick any client — form never calls it in bulk mode.
	var client silenceform.Client
	for _, t := range pending.tenants {
		if c, ok := p.clients[t]; ok {
			client = c
			break
		}
	}
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Client:     client,
			Styles:     styles,
			Now:        now,
			Creator:    creator,
			Bulk:       true,
			BulkBanner: banner,
		})
	})
}

// bulkSilenceBanner formats the form's banner string. Single-
// tenant + N=1 reads "applies to 1 alert (tenant prod)";
// otherwise "applies to N alerts across M tenants — each
// silenced with its own labels". Wording matches docs/design/
// bulk-silence.md so the user sees exactly what the submit will
// fan out to.
func bulkSilenceBanner(targets []bulkSilenceTarget, tenants []string) string {
	n := len(targets)
	if len(tenants) == 1 {
		alertWord := "alerts"
		if n == 1 {
			alertWord = "alert"
		}
		return fmt.Sprintf("applies to %d %s (tenant %s)", n, alertWord, tenants[0])
	}
	return fmt.Sprintf("applies to %d alerts across %d tenants — each silenced with its own labels", n, len(tenants))
}

// handleBulkSilenceConfirm consumes a ConfirmResultMsg from the
// pre-form confirm modal (N≥2 path). Yes pushes the bulk form;
// No / Cancelled drops the pending state silently. The single-
// row confirm also lands here when openExpireConfirmUnified-
// shaped flows ever need it on the alerts page; today there are
// no such, so the absence of pending state is a plain no-op.
func (p *Page) handleBulkSilenceConfirm(m modal.ConfirmResultMsg) tea.Cmd {
	pending := p.pendingBulkSilence
	if len(pending.targets) == 0 {
		return nil
	}
	if m.Cancelled || !m.Yes {
		p.pendingBulkSilence = pendingBulkSilence{}
		return nil
	}
	return p.pushBulkSilenceForm()
}

// bulkSilenceDoneMsg is the result envelope for a completed
// bulk-silence fanout. Successes carries the alert fingerprints
// whose CreateSilence returned nil — Update unmarks those rows;
// fingerprints absent from the list (failures or unstarted-due-
// cancel) keep their marks so retry is one keystroke. Total is
// the original target count so the flash can read "silenced N of
// Total".
type bulkSilenceDoneMsg struct {
	total     int
	successes []string
}

// handleBulkSilenceSubmit runs after the bulk form auto-pops on
// Ctrl+S submit. The user has filled the metadata (comment,
// starts/ends, creator) once; the page stamps it onto every
// pending target's matcher set and dispatches the fanout. The
// returned Cmd performs the worker-pool dispatch and emits
// bulkSilenceDoneMsg when every result has landed.
func (p *Page) handleBulkSilenceSubmit(m silenceform.BulkSubmittedMsg) tea.Cmd {
	pending := p.pendingBulkSilence
	p.pendingBulkSilence = pendingBulkSilence{}
	if len(pending.targets) == 0 {
		return nil
	}
	if p.cancelBulk != nil {
		// Cancel any prior in-flight round before starting a new one.
		// Idempotent: the prior round's deferred cancel() is a no-op
		// if it already ran.
		p.cancelBulk()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelBulk = cancel
	clients := p.clients
	concurrency := p.bulkConcurrency
	logger := p.logger
	targets := pending.targets
	spec := backend.SilenceSpec{
		StartsAt:  m.StartsAt,
		EndsAt:    m.EndsAt,
		CreatedBy: m.Creator,
		Comment:   m.Comment,
	}
	return func() tea.Msg {
		// Local cancel: releases this round's ctx subtree the moment
		// dispatch returns, regardless of whether p.cancelBulk has
		// since been overwritten by a newer round.
		defer cancel()
		successes := dispatchBulkSilence(ctx, clients, targets, spec, concurrency, logger)
		return bulkSilenceDoneMsg{
			total:     len(targets),
			successes: successes,
		}
	}
}

// handleBulkSilenceDone applies a completed bulk-silence fanout.
// Successes drop their marks; everything else (failures and
// unstarted-due-cancel) keeps its mark. Does not touch p.cancelBulk
// — that field may now refer to a newer round; the producing Cmd
// already deferred its own cancel().
func (p *Page) handleBulkSilenceDone(m bulkSilenceDoneMsg) tea.Cmd {
	for _, fp := range m.successes {
		delete(p.marks, fp)
	}
	failed := m.total - len(m.successes)
	return flashBulkSilenceResult(m.total, len(m.successes), failed)
}

// flashBulkSilenceResult formats the success / partial / total-
// failure flash text for a completed bulk-silence round. N=1 reads
// "silence created" (matching the single-row form's success flash);
// N≥2 uses count-based wording.
func flashBulkSilenceResult(total, success, failed int) tea.Cmd {
	if total == 1 {
		if success == 1 {
			return flashFn(footer.FlashSuccess, "silence created")
		}
		return flashFn(footer.FlashError, "silence failed")
	}
	if failed == 0 {
		return flashFn(footer.FlashSuccess, fmt.Sprintf("silenced %d alerts", success))
	}
	if success == 0 {
		return flashFn(footer.FlashError, fmt.Sprintf("silence failed for %d alerts", failed))
	}
	return flashFn(footer.FlashWarn, fmt.Sprintf("silenced %d of %d — %d failed", success, total, failed))
}

// silenceResult is the per-call outcome the worker pool emits
// onto the shared results channel. Tenant rides along for
// structured-log attribution on failure.
type silenceResult struct {
	fingerprint string
	tenant      string
	err         error
}

// dispatchBulkSilence runs the per-tenant fanout. Tenants run in
// parallel goroutines; inside each tenant a bounded worker pool
// of `min(concurrency, len(targets))` workers consumes from a
// per-tenant jobs channel. concurrency=1 collapses to fully
// sequential per tenant. Mirrors the silences page's
// dispatchBulkExpire shape — the only differences are the verb
// (CreateSilence vs ExpireSilence) and the result shape.
//
// Returns the alert fingerprints whose CreateSilence returned
// nil. The caller derives "failed = total - len(successes)"; that
// bucket includes both real errors and unstarted-due-cancel. Both
// keep their marks so the user can retry only the unfinished work.
func dispatchBulkSilence(
	ctx context.Context,
	clients map[string]silenceform.Client,
	targets []bulkSilenceTarget,
	specBase backend.SilenceSpec,
	concurrency int,
	logger *slog.Logger,
) []string {
	byTenant := map[string][]bulkSilenceTarget{}
	tenants := []string{}
	for _, t := range targets {
		if _, seen := byTenant[t.Tenant]; !seen {
			tenants = append(tenants, t.Tenant)
		}
		byTenant[t.Tenant] = append(byTenant[t.Tenant], t)
	}
	resCh := make(chan silenceResult, len(targets))
	var tenantWg sync.WaitGroup
	for _, tenant := range tenants {
		client, ok := clients[tenant]
		group := byTenant[tenant]
		if !ok {
			for _, t := range group {
				resCh <- silenceResult{
					fingerprint: t.Fingerprint,
					tenant:      tenant,
					err:         errors.New("no writeable backend for tenant"),
				}
			}
			continue
		}
		tenantWg.Add(1)
		go func(tenant string, ts []bulkSilenceTarget, c silenceform.Client) {
			defer tenantWg.Done()
			runTenantSilencePool(ctx, tenant, ts, c, specBase, concurrency, resCh)
		}(tenant, group, client)
	}
	go func() {
		tenantWg.Wait()
		close(resCh)
	}()
	successes := make([]string, 0, len(targets))
	for r := range resCh {
		if r.err == nil {
			successes = append(successes, r.fingerprint)
			continue
		}
		if logger != nil {
			logger.Error("bulk silence: alert silence failed",
				slog.String("backend", r.tenant),
				slog.String("tenant", r.tenant),
				slog.String("alert_fingerprint", r.fingerprint),
				slog.String("err", r.err.Error()),
			)
		}
	}
	return successes
}

// runTenantSilencePool is the per-tenant bounded worker pool.
// Producer feeds the jobs channel under ctx.Done so a Close()
// mid-flight stops dispatching new work; consumers run
// CreateSilence and emit results regardless of the ctx state for
// jobs they've already pulled, so an in-flight request completes
// naturally. Workers cap at min(concurrency, len(targets)).
func runTenantSilencePool(
	ctx context.Context,
	tenant string,
	targets []bulkSilenceTarget,
	client silenceform.Client,
	specBase backend.SilenceSpec,
	concurrency int,
	resCh chan<- silenceResult,
) {
	workers := max(min(concurrency, len(targets)), 1)
	jobs := make(chan bulkSilenceTarget)
	go func() {
		defer close(jobs)
		for _, t := range targets {
			select {
			case <-ctx.Done():
				return
			case jobs <- t:
			}
		}
	}()
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for t := range jobs {
				spec := specBase
				spec.Matchers = t.Matchers
				_, err := client.CreateSilence(ctx, spec)
				resCh <- silenceResult{fingerprint: t.Fingerprint, tenant: tenant, err: err}
			}
		})
	}
	wg.Wait()
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

// handleClearMarks drops every mark on the page in response to
// the global Ctrl+\ binding. Flashes "marks cleared" when the
// pre-clear count was non-zero so the user sees confirmation;
// silently no-ops otherwise (no flash on a key that did nothing
// would be a poor affordance, but an unconditional flash on a
// page that never had marks would be surprising spam).
func (p *Page) handleClearMarks() tea.Cmd {
	if len(p.marks) == 0 {
		return nil
	}
	p.marks = map[string]struct{}{}
	return flashFn(footer.FlashInfo, "marks cleared")
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
	tf := p.timeFormat
	return app.PushPage(func() app.Page {
		return alert.New(alert.Options{
			Alert:      entry.a,
			Tenant:     entry.tenant,
			Styles:     styles,
			Now:        now,
			Clients:    clients,
			Creator:    creator,
			TimeFormat: tf,
		})
	})
}

// View implements app.Page.
func (p *Page) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	p.bodyHeight = height - 1 // header takes one line; the rest is table-row budget
	if len(p.view) == 0 {
		// Render bg-less so the empty state matches the regular
		// table view's framing — both use the terminal default
		// background. styles.Body.Default would paint the body
		// palette behind the empty pane, which renders as a
		// coloured patch the populated view doesn't have, breaking
		// the visual parity between "loading" and "loaded" frames.
		return lipgloss.NewStyle().Width(width).Height(height).Render(p.emptyState())
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1)
	body := headerLine + "\n" + rows
	return lipgloss.NewStyle().Width(width).Render(body)
}

// halfPageStep returns the Ctrl+D / Ctrl+U distance: half the
// rendered body height, with a 10-row cold-start fallback so a
// keystroke that beats the first render still moves a sane amount.
// Floored at 1 — a future reviewer narrowing the guard must not
// silently turn the binding into a no-op.
func (p *Page) halfPageStep() int {
	if p.bodyHeight < 2 {
		return 10
	}
	return max(p.bodyHeight/2, 1)
}

// fullPageStep returns the Ctrl+F / Ctrl+B distance: a full body
// minus two lines of context (vim's CTRL-F convention), with a
// 20-row cold-start fallback. Floored at 1 for the same reason as
// halfPageStep.
func (p *Page) fullPageStep() int {
	if p.bodyHeight < 4 {
		return 20
	}
	return max(p.bodyHeight-2, 1)
}

// emptyState is the body content shown when no alerts match. Two
// branches: "we polled and there's nothing" vs. "filter hides
// everything" — the second is actionable, the first isn't.
func (p *Page) emptyState() string {
	if p.filter != "" || p.stateFilter != "" {
		return "no alerts match the active filter — Esc clears the prompt, Shift+F cycles state filters"
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
	cols := []string{sortKeySeverity, sortKeyName, sortKeyState, sortKeyAge}
	widths := p.columnWidths(width)
	// fg-only renderers so the header keeps the terminal default
	// background — painting palette bg inside the unstyled body
	// frame creates a coloured stripe (see feedback memory on
	// chrome rendering).
	headerFg := theme.FgOnly(p.styles.Table.Header.GetForeground())
	activeFg := theme.FgOnly(p.styles.Table.HeaderActive.GetForeground())

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", rowPrefixCols))
	idx := 0
	if p.showTenantColumn() && idx < len(widths) {
		b.WriteString(headerFg.Render(padRight("TENANT", widths[idx])))
		idx++
	}
	for _, k := range cols {
		if idx >= len(widths) {
			break
		}
		label := strings.ToUpper(k)
		if arrow := p.sorter.ArrowFor(k); arrow != "" {
			label = label + " " + arrow
		}
		padded := padRight(label, widths[idx])
		// Active column gets HeaderActive; the rest get the regular
		// Header foreground. The two tints plus the arrow glyph give
		// two distinct cues for "which sort is live" — one for the
		// eye scanning columns, one for the eye reading the arrow.
		if p.sorter.IsActive(k) {
			b.WriteString(activeFg.Render(padded))
		} else {
			b.WriteString(headerFg.Render(padded))
		}
		idx++
	}
	return b.String()
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
		ageLabel := p.formatTime(a.StartsAt)
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
			// k9s parity: cursor bg tracks the row's semantic
			// colour (severity), not the static cursorBgColor.
			// `select_table.go:128` in k9s replaces the selected
			// style on every selection-changed event; this is the
			// equivalent.
			rowColor := severityStyle(a, p.styles).GetForeground()
			line = p.styles.Table.CursorOver(rowColor).Render(line)
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
// has 5 entries instead of 4. AGE is widened in absolute mode so
// the ISO local timestamp ("2026-05-01 13:45:00", 19 cols) fits
// without truncation per Q7.4.
func (p *Page) padColumns(parts []string, width int) string {
	cols := p.columnWidths(width)
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padRight(v, cols[i]))
	}
	return b.String()
}

// columnWidths returns the per-column widths (TENANT optional,
// then SEVERITY, ALERTNAME flex, STATE, AGE). Extracted so the
// header renderer can pad each label to its own column width
// before applying per-cell styling — padColumns concatenates the
// raw padded strings, but per-cell styling needs each cell's
// width separately.
func (p *Page) columnWidths(width int) []int {
	tenantCol := 0
	if p.showTenantColumn() {
		tenantCol = 16
	}
	const sevCol, stateCol = 12, 14
	ageCol := 12
	if p.timeFormat == app.TimeFormatAbsolute {
		ageCol = 20
	}
	flex := max(width-tenantCol-sevCol-stateCol-ageCol-rowPrefixCols, 10)

	cols := make([]int, 0, 5)
	if tenantCol > 0 {
		cols = append(cols, tenantCol)
	}
	cols = append(cols, sevCol, flex, stateCol, ageCol)
	return cols
}

// formatTime renders ts according to the page's active time
// format. Mirrors the silences / alert-detail formatters so the
// three views agree on how the toggle reads.
func (p *Page) formatTime(ts time.Time) string {
	if p.timeFormat == app.TimeFormatAbsolute {
		return header.FormatAbsolute(ts)
	}
	return header.FormatAge(p.now(), ts)
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
	p.sorter.Apply(p.view)

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

// SPDX-License-Identifier: Apache-2.0

// Package silences renders the silences list page. The page
// surfaces the Silence write actions (new, edit, expire, editor)
// behind Dangerous bindings so read-only mode hides them all.
//
// Single-row writes (`n`, `e`) operate on the cursor row. The
// expire verb `x` follows the k9s same-key-different-N rule: with
// no marks it expires the cursor row (existing wording); with one
// or more marks it bulk-expires every marked silence via a per-
// tenant bounded worker pool (defaults.bulk_concurrency, default
// 4). Destructive verbs always round-trip through a confirm modal
// with the default-No safe choice so a stray Enter never destroys
// data. `Ctrl+E` opens the silence in $EDITOR (or $A10R_EDITOR)
// for free-form editing.
package silences

import (
	"context"
	"errors"
	"fmt"
	"image/color"
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
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	silencepage "github.com/wilfriedroset/a10r/internal/tui/page/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Sort column keys. Stable identifiers passed to the tablesort
// helper. Order does not matter at the constant level — visual
// h/l walk order comes from the silenceSortColumns slice (BY,
// STARTS, ENDS, STATE) since BY is leftmost on the rendered table
// while ENDS is the page default.
const (
	sortKeyEndsAt    = "ends"
	sortKeyStartsAt  = "starts"
	sortKeyCreatedBy = "by"
	sortKeyState     = "state"
)

// silenceSortColumns returns the page's sortable axes in visual
// header order (BY → STARTS → ENDS → STATE). h/l walks through
// this order so "right one column" matches what the user sees;
// ENDS is the default sort key — silences expiring soonest at
// the top per E2 — but that lives separately from registration
// order. All defaults are ASC.
func silenceSortColumns() []tablesort.Column[silenceEntry] {
	return []tablesort.Column[silenceEntry]{
		{
			Key: sortKeyCreatedBy, Title: "BY", Hotkey: 'C', DefaultAsc: true,
			Description: "sort by creator",
			Less:        func(a, b silenceEntry) bool { return a.s.CreatedBy < b.s.CreatedBy },
		},
		{
			Key: sortKeyStartsAt, Title: "STARTS", Hotkey: 'S', DefaultAsc: true,
			Description: "sort by startsAt",
			Less:        func(a, b silenceEntry) bool { return a.s.StartsAt.Before(b.s.StartsAt) },
		},
		{
			Key: sortKeyEndsAt, Title: "ENDS", Hotkey: 'E', DefaultAsc: true,
			Description: "sort by endsAt",
			Less:        func(a, b silenceEntry) bool { return a.s.EndsAt.Before(b.s.EndsAt) },
		},
		{
			Key: sortKeyState, Title: "STATE", Hotkey: 'T', DefaultAsc: true,
			Less: func(a, b silenceEntry) bool { return a.s.State < b.s.State },
		},
	}
}

// Client is the write surface the silences page needs: it pushes
// the silence form (silenceform.Client) on `n` / `e` and calls
// ExpireSilence on `x` (cursor or bulk, depending on marks).
// Bundled at the page level rather than added to silenceform.Client
// because expire isn't part of the form's contract — the form
// never expires anything. backend.Client satisfies this interface
// for free; tests inject a small fake.
type Client interface {
	silenceform.Client
	ExpireSilence(ctx context.Context, id string) error
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

	// bodyHeight is the table-row capacity snapshotted on the most
	// recent View — body height minus the column-header line.
	// Powers viewport-aware Ctrl+D/U/F/B steps; zero before the
	// first render so handlers fall back to 10 / 20 for the very
	// first keystroke. See alerts.Page for the rationale.
	bodyHeight int

	// sorter owns the active sort column + direction. Comparators
	// and column metadata come from silenceSortColumns; the helper
	// applies the cycle / flip / walk convention.
	sorter  *tablesort.Sorter[silenceEntry]
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
	clients map[string]Client
	// creator seeds the form's CreatedBy field; usually $USER.
	creator string

	// marks is the set of silence IDs the user has Space-toggled
	// for bulk operations (Ctrl+X bulk expire). Tracking by ID,
	// like the alerts page tracks by Fingerprint, so marks survive
	// re-sorts and re-filters without sliding onto unrelated
	// silences.
	marks map[string]struct{}

	// pendingExpire stores the IDs queued for an open expire
	// confirm modal so the ConfirmResultMsg handler knows which
	// silences to expire on Yes. ids is empty between confirm
	// rounds. bulk distinguishes single-row x from Ctrl+X for the
	// flash wording on the result.
	pendingExpire pendingExpire

	// editor handles the `Ctrl+E` round-trip via $EDITOR. Empty
	// (zero-value) Resolver flashes a hint when the user invokes
	// the binding so the affordance fails loudly rather than
	// crashing.
	editor edit.Resolver

	// timeFormat mirrors the app-global toggle (relative vs.
	// absolute timestamps). Flipped by app.TimeFormatChangedMsg
	// so every list page agrees.
	timeFormat app.TimeFormat

	// pendingEdit captures which silence the user is editing in
	// $EDITOR so the FinishedMsg handler can call UpdateSilence
	// against the right backend. Empty between rounds.
	pendingEdit pendingEdit

	// bulkConcurrency caps the per-tenant worker pool for the
	// bulk-expire fanout. Tenants always run in parallel; this
	// knob limits the inner pool size per tenant. Resolved from
	// Options.BulkConcurrency (zero falls back to the config
	// default at construction time).
	bulkConcurrency int
	// logger is the structured logger used for per-failure detail
	// in the bulk fanout. Nil suppresses logging — the page never
	// crashes on a missing logger.
	logger *slog.Logger
	// cancelBulk cancels the in-flight bulk-expire fanout when set.
	// Populated by handleExpireConfirm at fanout start; cleared
	// when the bulkExpireDoneMsg lands. Close() calls it so a page
	// pop short-circuits not-yet-started workers.
	cancelBulk context.CancelFunc

	// polledTenants is the set of tenants that have produced at
	// least one DataMsg in this page's lifetime. The page's
	// "have we polled?" check reads it through the active scope
	// — see polled(). Storing a per-tenant set rather than a
	// global bool avoids a flash of "no silences (yet)" in a
	// multi-backend setup with a single-tenant scope: a fast
	// out-of-scope tenant returning [] would otherwise flip the
	// page out of loading state before the in-scope tenant has
	// answered, briefly painting the empty-list copy under a
	// title that already counts zero rows.
	polledTenants map[string]struct{}
	// nextRefresh is the per-tenant DataMsg.NextAt timestamp.
	// The bottom-border Footer collapses it into "next refresh
	// 25s" by picking the soonest entry across in-scope tenants —
	// the user wants the timer that fires next, not a per-tenant
	// table.
	nextRefresh map[string]time.Time
	// refreshing is true between an `r` press and the next
	// in-scope poll.DataMsg arrival so the renderer can keep the
	// spinner up while the caller's nudge is in flight. Cleared
	// only on the first in-scope DataMsg afterward — an out-of-
	// scope tenant's reply doesn't count as the user's manual
	// refresh having landed.
	refreshing bool
	// spinner is the cold-start / refresh-in-flight indicator
	// (bubbles `Points` — three dots cycling). Stopped (i.e. its
	// Tick chain is broken) outside of those two windows; see
	// the spinner.TickMsg branch in Update.
	spinner spinner.Model
}

// pendingEdit is the in-flight state between an opened editor
// session and its FinishedMsg. id is the silence ID; tenant is
// the backend the silence belongs to (cached at open time so a
// poll-tick reordering between open and save still routes the
// update correctly).
type pendingEdit struct {
	id     string
	tenant string
}

// pendingExpire is the in-flight state between an opened expire
// confirm modal and its ConfirmResultMsg. ids is the set of
// silence IDs to expire on Yes, paired with the tenant the
// silence lives on (resolved at modal-open time so a poll-tick
// reordering or filter change between open and Yes never expires
// a different silence on a different backend). bulk picks the
// flash wording.
type pendingExpire struct {
	ids  []pendingExpireID
	bulk bool
}

// pendingExpireID pairs a silence ID with its tenant so the
// confirm-result handler can route ExpireSilence without
// re-reading the live view.
type pendingExpireID struct {
	id     string
	tenant string
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
	Clients map[string]Client
	// Creator is the default value the silence form opens with —
	// usually $USER. Empty falls back to "a10r".
	Creator string
	// EditorResolver handles the `Ctrl+E` round-trip. Zero value
	// flashes a "no editor configured" hint when the user invokes
	// the binding. Production wiring passes edit.SystemResolver();
	// tests inject a recording resolver.
	EditorResolver edit.Resolver
	// TimeFormat seeds the page's time-format mode at construction
	// so a page pushed *after* the user toggled `t` doesn't open
	// in relative while the rest of the app reads absolute.
	TimeFormat app.TimeFormat
	// BulkConcurrency caps the per-tenant worker pool for the
	// bulk-expire fanout. Zero resolves to config.DefaultBulkConcurrency
	// at construction time so callers can pass the unmaterialised
	// `defaults.bulk_concurrency` directly.
	BulkConcurrency int
	// Logger receives per-failure detail (`backend`, `tenant`,
	// `silence_id`, `err`) at error level when the bulk fanout
	// surfaces individual ExpireSilence failures. Nil suppresses
	// logging — the page never crashes on a missing logger.
	Logger *slog.Logger
}

// New constructs an empty silences page.
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
		clients:         opts.Clients,
		creator:         opts.Creator,
		editor:          opts.EditorResolver,
		timeFormat:      opts.TimeFormat,
		byTenant:        map[string][]backend.Silence{},
		marks:           map[string]struct{}{},
		sorter:          tablesort.New(silenceSortColumns(), sortKeyEndsAt),
		scope:           scopeAll,
		nextRefresh:     map[string]time.Time{},
		polledTenants:   map[string]struct{}{},
		spinner:         sp,
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
	}
}

// hintNoWriteableBackend is the shared message every write action
// flashes when no silenceform.Client is reachable. Mirrors the
// alerts / alert / groups pages so the affordance reads
// identically across resources.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

// scopeAll is the canonical "every configured tenant" label.
// Pinned as a constant so the title, scopeIncludes, and the
// global numeric quick-switch agree on the spelling.
const scopeAll = "all"

// Init implements app.Page. Kicks the spinner so the cold-start
// "loading" empty state animates while the first poll tick
// resolves. Once a DataMsg lands, the spinner's Tick chain is
// broken (see Update) so it stops re-issuing without a separate
// timer to manage.
func (p *Page) Init() tea.Cmd { return p.spinner.Tick }

// Close implements app.Page. Cancels any in-flight bulk-expire
// fanout so a page-pop while workers are mid-air aborts not-yet-
// started work via the worker channel select. In-flight HTTP
// requests are allowed to finish; expire is idempotent on the AM
// side so completing them is safe.
func (p *Page) Close() tea.Cmd {
	if p.cancelBulk != nil {
		p.cancelBulk()
		p.cancelBulk = nil
	}
	return nil
}

// Crumb implements app.Page.
func (*Page) Crumb() string { return "silences" }

// Title implements app.Page. Mirrors the alerts page's shape:
// `silences(<scope>)[<count>]` or `silences(<scope>)[F/T]` while
// a filter is active. While the page is in a loading window —
// cold start (no DataMsg yet) or a manual `r` refresh in flight —
// the title flips to the spinner-led "loading silences…" so the
// border itself reads as the loading affordance, k9s-style. The
// title swaps back to the count form on the next DataMsg.
func (p *Page) Title() string {
	if !p.polled() || p.refreshing {
		return p.spinner.View() + " loading silences…"
	}
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
// filter and the bulk-mark count so the user can spot what's
// been applied or queued without re-opening the prompt. Cadence
// (next-refresh deadline) rides the bordered body's bottom edge
// via Footer — it's ambient frame state, not a header subtitle.
// Time-format toggle is intentionally absent: the flash on `t`
// press is the affordance signal, and the visible cell content
// (relative vs absolute) is self-evident — adding a subtitle
// here would steal a body row of real estate.
func (p *Page) HeaderContent() string {
	var parts []string
	if p.filter != "" {
		parts = append(parts, "filter:"+p.filter)
	}
	if n := len(p.marks); n > 0 {
		parts = append(parts, fmt.Sprintf("marked:%d", n))
	}
	return strings.Join(parts, " · ")
}

// Footer implements app.Page. Renders the next-refresh deadline
// — or "refreshing…" while a manual `r` is in flight — into the
// bordered body's bottom edge, k9s-style symmetry with the
// title in the top edge. Returns empty pre-poll so the cold-
// start frame stays quiet (the spinner in the body already says
// "loading"). Drawn from the soonest in-scope DataMsg.NextAt:
// the operator cares about the next tick that fires, not a per-
// tenant table. Past-due reads "due" so a slow loop never
// flashes a negative duration.
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
// in-scope tenants — the next moment fresh data will land. Zero
// when no in-scope tenant has published a NextAt.
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

// nextRefreshLabel formats the "25s" / "due" / "3m" segment for
// the bottom-border footer. Past-due is rendered as "due" so a
// slow tick or paused loop reads honestly without flashing a
// negative duration.
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

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache only replays "silences" payloads into this
// page on push — alerts / receivers / groups cache entries are
// filtered out before the page would have to type-assert and
// discard them.
func (*Page) PollResources() []string { return []string{"silences"} }

// Bindings implements app.Page. Every write action carries
// Dangerous so read-only mode (C4) hides them via the action
// registry. `x` doubles as "expire cursor row" (no marks) and
// "bulk expire all marked rows" (one or more marks) — k9s-style
// same-key-different-N. Ctrl+X is intentionally absent; the
// single-binding rule is the whole point of this page's bulk UX.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("silences")
	out := make([]action.Action, 0, 8+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "detail", View: "silences"},
		action.Action{Key: "n", Description: "new", View: "silences", Dangerous: true},
		action.Action{Key: "e", Description: "edit", View: "silences", Dangerous: true},
		action.Action{Key: "x", Description: "expire (cursor / marks)", View: "silences", Dangerous: true},
		action.Action{Key: "Space", Description: "mark", View: "silences"},
		action.Action{Key: "Ctrl+E", Description: "editor", View: "silences", Dangerous: true},
		action.Action{Key: "Ctrl+N", Description: "recreate (expired)", View: "silences", Dangerous: true},
	)
	out = append(out, sortBindings...)
	// `r` is documented in the global help catalog; the page hint
	// strip surfaces it here so the affordance also shows up next
	// to the page-specific verbs.
	out = append(out, action.Action{Key: "r", Description: "refresh", View: "silences"})
	return out
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.handleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.DataMsg:
		s, ok := m.Resource.([]backend.Silence)
		if !ok {
			return p, nil
		}
		p.byTenant[m.Tenant] = s
		// Capture the poll's NextAt so the bottom-border Footer can
		// render "next refresh 25s" without a parallel ticker.
		// Zero-valued (legacy / test) DataMsgs leave the per-tenant
		// entry intact rather than clobbering it with a zero —
		// keeps the footer stable when a unit test fakes only the
		// resource payload.
		if !m.NextAt.IsZero() {
			p.nextRefresh[m.Tenant] = m.NextAt
		}
		p.polledTenants[m.Tenant] = struct{}{}
		// Only clear refreshing once an in-scope tenant has
		// answered — an out-of-scope DataMsg arriving during a
		// manual `r` window would otherwise drop the spinner
		// before the user has actually seen fresh data for the
		// scope they're looking at.
		if p.scopeIncludes(m.Tenant) {
			p.refreshing = false
		}
		p.recompute()
		return p, nil
	case spinner.TickMsg:
		// Forward only while the spinner is meaningful. Outside the
		// cold-start / refresh-in-flight windows we drop the tick,
		// which breaks the spinner's self-perpetuating Tick chain
		// and stops the per-frame redraw cost.
		if !p.spinnerActive() {
			return p, nil
		}
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(m)
		return p, cmd
	case silenceform.SubmittedMsg, silenceform.CancelledMsg, modal.ConfirmResultMsg, bulkExpireDoneMsg:
		_ = m // multi-type case: handleWriteResult consumes msg via its own type switch
		cmd := p.handleWriteResult(msg)
		return p, cmd
	case edit.FinishedMsg:
		cmd := p.handleEditorFinished(m)
		return p, cmd
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.handleFilterPrompt(m)
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleWriteResult dispatches the four messages emitted by the
// page's write-action machinery (silence form submit / cancel,
// confirm modal result, bulk-expire fanout result). Pulled out of
// Update so the main switch stays under the cyclop budget.
func (p *Page) handleWriteResult(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash so the user has visual
		// confirmation. The next poll tick surfaces the change in
		// the list. Updated picks "updated" vs. "created" so an
		// edit doesn't read like a duplicate creation.
		verb := "created"
		if m.Updated {
			verb = "updated"
		}
		return flashFn(footer.FlashSuccess, "silence "+verb+": "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. No flash — form Esc is a
		// non-event from the user's perspective.
		return nil
	case modal.ConfirmResultMsg:
		return p.handleExpireConfirm(m)
	case bulkExpireDoneMsg:
		return p.handleBulkExpireDone(m)
	}
	return nil
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
	newCursor, handled := cursor.HandleMotion(
		m.String(),
		p.cursor,
		len(p.view),
		cursor.HalfPageStep(p.bodyHeight),
		cursor.FullPageStep(p.bodyHeight),
	)
	if !handled {
		return false
	}
	p.cursor = newCursor
	p.snapshotFocus()
	return true
}

// handleSort processes sort-column shortcuts (h/l walk plus
// Shift+letter direct shortcuts). Returns true when the key was a
// sort change.
//
// Direction semantics mirror the alerts page: pressing the active
// column's shortcut again flips ASC/DESC; pressing a different
// column's shortcut resets to that column's default direction. h/l
// walk also resets to default for the new column.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	if !p.sorter.HandleKey(m.String()) {
		return false
	}
	// User-initiated re-sort is k9s-positional: cursor stays at the
	// same row index, whichever silence lands under it becomes the
	// new focus. Clearing focusID before recompute bypasses the
	// find-by-ID branch; snapshotFocus then re-captures the new
	// focus so subsequent poll / scope / filter recomputes still
	// follow it content-stably.
	p.focusID = ""
	p.recompute()
	return true
}

func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "enter":
		cmd := p.drillToDetail()
		return p, cmd
	case "n":
		cmd := p.openNewSilenceForm()
		return p, cmd
	case "e":
		cmd := p.openEditSilenceForm()
		return p, cmd
	case "x":
		cmd := p.openExpireConfirmUnified()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "ctrl+e":
		cmd := p.openEditorForCursor()
		return p, cmd
	case "ctrl+n":
		cmd := p.openRecreateSilenceForm()
		return p, cmd
	case "r":
		cmd := p.requestRefresh()
		return p, cmd
	}
	return p, nil
}

// drillToDetail returns a Cmd that pushes the silence-detail page
// for the row under the cursor. Empty view falls through to a soft
// Info flash so the user sees a reason for the no-op.
func (p *Page) drillToDetail() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	entry := p.view[p.cursor]
	styles := p.styles
	return app.PushPage(func() app.Page {
		return silencepage.New(silencepage.Options{
			Silence: entry.s,
			Tenant:  entry.tenant,
			Styles:  styles,
		})
	})
}

// openExpireConfirmUnified is the entry point for the `x` key.
// k9s-style: marks-if-any, cursor-row otherwise. Routing the same
// key through one helper keeps the user's muscle memory shared
// across both flows; the underlying confirm machinery still picks
// the appropriate question wording from the count.
func (p *Page) openExpireConfirmUnified() tea.Cmd {
	if len(p.marks) == 0 {
		return p.openExpireConfirm()
	}
	return p.openBulkExpireConfirm()
}

// requestRefresh emits a RefreshRequestedMsg so the wiring layer
// can poke the silences pollers, flips the page into refreshing
// state, and (re)kicks the spinner Tick chain. Idempotent in
// practice: a second `r` press while still refreshing simply
// re-emits the message (the poll layer coalesces nudges into a
// single buffered slot) and re-issues a Tick the spinner ignores
// because it already has one in flight via its tag mechanism.
func (p *Page) requestRefresh() tea.Cmd {
	p.refreshing = true
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "silences", Scope: scope}
	}
	return tea.Batch(emit, p.spinner.Tick)
}

// spinnerActive reports whether the spinner should continue to
// animate. Two windows: cold start (no in-scope DataMsg yet) and
// refresh-in-flight (between an `r` press and the next DataMsg).
// Outside those, the page draws static "next refresh" timing.
func (p *Page) spinnerActive() bool { return !p.polled() || p.refreshing }

// polled reports whether at least one in-scope tenant has
// produced a DataMsg. Read by Title / Footer / emptyState to
// pick between the loading affordance and the populated frame.
// Scope-aware so a fast out-of-scope tenant in a multi-backend
// setup doesn't flip the page out of loading state before the
// in-scope tenant has answered — the user would otherwise see
// "no silences (yet)" flash under a "silences(primary)[0]"
// title while waiting for primary's first poll.
func (p *Page) polled() bool {
	for tenant := range p.polledTenants {
		if p.scopeIncludes(tenant) {
			return true
		}
	}
	return false
}

// handleClearMarks drops every mark on the page in response to
// the global Ctrl+\ binding. Flashes "marks cleared" when the
// pre-clear count was non-zero so the user sees confirmation;
// silently no-ops otherwise.
func (p *Page) handleClearMarks() tea.Cmd {
	if len(p.marks) == 0 {
		return nil
	}
	p.marks = map[string]struct{}{}
	return flashFn(footer.FlashInfo, "marks cleared")
}

// toggleMarkAtCursor flips the mark on the cursor row. No-op on
// an empty view; silences without an ID are silently skipped
// (defensive — every backend.Silence the v2 API returns has one).
func (p *Page) toggleMarkAtCursor() {
	if p.cursor >= len(p.view) {
		return
	}
	id := p.view[p.cursor].s.ID
	if id == "" {
		return
	}
	if _, ok := p.marks[id]; ok {
		delete(p.marks, id)
		return
	}
	p.marks[id] = struct{}{}
}

// openEditSilenceForm pushes the silence form in edit mode
// prefilled from the cursor row. Selection rule: the cursor
// row's tenant — `e` operates on the visible row, never falls
// back to "first in-scope" the way `n` does because there's no
// row to edit if no row is focused.
func (p *Page) openEditSilenceForm() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	entry := p.view[p.cursor]
	client, ok := p.clients[entry.tenant]
	if !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	creator := entry.s.CreatedBy
	if creator == "" {
		creator = p.creator
	}
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	s := entry.s
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Client:   client,
			Styles:   styles,
			Now:      now,
			Creator:  creator,
			Matchers: s.Matchers,
			Comment:  s.Comment,
			EndsAt:   s.EndsAt,
			EditID:   s.ID,
		})
	})
}

// recreateFormOptions assembles the silenceform.Options for a
// recreate-expired flow against the cursor row. On a recreatable
// row it returns (opts, nil, true); on every refusal it returns
// (zero, flash, false) — flash is the Cmd the caller surfaces.
// Funnelling the refusal flashes through the same helper that owns
// the guards keeps the handler from drifting: a new refusal added
// here surfaces in openRecreateSilenceForm without further wiring.
//
// The returned Options pin Matchers + Comment from the source
// silence verbatim, set Creator from the page (current user, NOT the
// original silence's CreatedBy — recreate is a new silence with new
// authorship), set BlankEnds + FocusEnds so the user lands on Ends
// with no "2h" footgun, and leave EditID empty so submit fires
// CreateSilence rather than UpdateSilence.
func (p *Page) recreateFormOptions() (silenceform.Options, tea.Cmd, bool) {
	if p.cursor >= len(p.view) {
		return silenceform.Options{}, flashFn(footer.FlashInfo, "no silence under the cursor"), false
	}
	if len(p.clients) == 0 {
		return silenceform.Options{}, flashFn(footer.FlashWarn, hintNoWriteableBackend), false
	}
	entry := p.view[p.cursor]
	if entry.s.State != backend.SilenceStateExpired {
		return silenceform.Options{}, flashFn(footer.FlashInfo,
			"only expired silences can be recreated — use `e` to edit a live silence"), false
	}
	client, ok := p.clients[entry.tenant]
	if !ok {
		return silenceform.Options{}, flashFn(footer.FlashWarn, hintNoWriteableBackend), false
	}
	return silenceform.Options{
		Client:    client,
		Styles:    p.styles,
		Now:       p.now,
		Creator:   p.defaultCreator(),
		Matchers:  entry.s.Matchers,
		Comment:   entry.s.Comment,
		BlankEnds: true,
		FocusEnds: true,
	}, nil, true
}

// openRecreateSilenceForm pushes the silence form prefilled from an
// expired cursor row, or surfaces the matching refusal flash. Each
// refusal's wording is owned by recreateFormOptions so the two
// callers (handler + tests) read the same source of truth.
func (p *Page) openRecreateSilenceForm() tea.Cmd {
	opts, refusal, ok := p.recreateFormOptions()
	if !ok {
		return refusal
	}
	return app.PushPage(func() app.Page { return silenceform.New(opts) })
}

// openExpireConfirm queues the cursor silence for expiration and
// opens the confirm modal. ID + tenant are captured into
// p.pendingExpire now so a poll tick that reorders rows or
// changes the active filter between Open and Yes can't expire a
// different silence than the one the user was looking at.
func (p *Page) openExpireConfirm() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	entry := p.view[p.cursor]
	if _, ok := p.clients[entry.tenant]; !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	p.pendingExpire = pendingExpire{
		ids:  []pendingExpireID{{id: entry.s.ID, tenant: entry.tenant}},
		bulk: false,
	}
	question := "expire silence " + entry.s.ID + "?"
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultNo)
	})
}

// openBulkExpireConfirm queues every marked silence for bulk
// expiration. Walks p.byTenant (not p.view) so a marked silence
// hidden by an active filter still gets expired — the user
// marked it deliberately and a filter change shouldn't silently
// drop it from the queue. Empty marks → soft Info flash hinting
// at the Space binding so the user discovers the affordance.
//
// Question wording matches docs/design/bulk-silence.md: a single
// queued silence keeps the existing single-row "expire silence
// <id>?" wording (functionally identical to the cursor-row path);
// two-or-more uses "expire N silences? (tenant <breakdown>)" so
// the user can see at a glance how many backends the fanout will
// touch. Default-No because expire is mostly-irreversible — the
// next poll re-fires the alert and on-call may page.
func (p *Page) openBulkExpireConfirm() tea.Cmd {
	if len(p.marks) == 0 {
		return flashFn(footer.FlashInfo, "no rows marked — Space marks one")
	}
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	ids := make([]pendingExpireID, 0, len(p.marks))
	for tenant, sils := range p.byTenant {
		for _, s := range sils {
			if _, m := p.marks[s.ID]; m {
				ids = append(ids, pendingExpireID{id: s.ID, tenant: tenant})
			}
		}
	}
	if len(ids) == 0 {
		// Marks survived but every silence vanished from byTenant
		// (every backend re-emitted without them). Defensive:
		// flash and clear so the user can re-mark.
		return flashFn(footer.FlashInfo, "no marked silences remain")
	}
	// Sort by ID for deterministic confirm-question wording and
	// stable iteration order across runs / tests.
	sort.Slice(ids, func(i, j int) bool { return ids[i].id < ids[j].id })
	p.pendingExpire = pendingExpire{ids: ids, bulk: true}
	var question string
	if len(ids) == 1 {
		question = "expire silence " + ids[0].id + "?"
	} else {
		question = fmt.Sprintf("expire %d silences? (tenant %s)", len(ids), formatTenantBreakdown(ids))
	}
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultNo)
	})
}

// formatTenantBreakdown renders the per-tenant count for the
// bulk-expire confirm modal. Single tenant returns the bare name
// (`"prod"`); multi-tenant returns a comma-joined `name=count`
// sequence sorted alphabetically by tenant for stable wording
// across runs (`"prod=12, staging=3"`).
func formatTenantBreakdown(ids []pendingExpireID) string {
	counts := map[string]int{}
	tenants := []string{}
	for _, id := range ids {
		if _, seen := counts[id.tenant]; !seen {
			tenants = append(tenants, id.tenant)
		}
		counts[id.tenant]++
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

// bulkExpireDoneMsg is the result envelope for a completed
// bulk-expire fanout. Successes carries the silence IDs whose
// ExpireSilence returned nil — Update unmarks those rows; the
// IDs that don't appear (failures or unstarted-due-cancel) keep
// their marks so retry is one keystroke. Total is the original
// queue size so the flash can read "expired N of Total".
type bulkExpireDoneMsg struct {
	bulk      bool
	total     int
	successes []string
}

// handleExpireConfirm consumes a ConfirmResultMsg arriving after
// an expire confirm modal. Yes kicks off the bulk-expire fanout
// (per-tenant bounded worker pool); the resulting bulkExpireDoneMsg
// arrives on Update and applies the unmark + flash. No / Cancelled
// clears the pending state silently. Tenants are read directly
// from the captured pair — no live-view lookup — so a poll tick
// or filter change between Open and Yes never reroutes the expire
// to the wrong backend or drops it as "unknown".
//
// Cancellation: a fresh context.Context is created per round and
// stored in p.cancelBulk. Close() on the page calls it; workers
// see the cancellation via the worker-channel select and exit
// without processing remaining IDs. The Cmd defers its own
// cancel() so a completed round releases its ctx without the
// done-handler having to look at p.cancelBulk — that field always
// refers to the *latest* round, not the one whose done message we
// happen to be processing. Any in-flight ExpireSilence runs to
// completion — expire is idempotent on the AM side, so finishing
// a request mid-cancel doesn't risk double-effect.
func (p *Page) handleExpireConfirm(m modal.ConfirmResultMsg) tea.Cmd {
	pending := p.pendingExpire
	p.pendingExpire = pendingExpire{}
	if m.Cancelled || !m.Yes || len(pending.ids) == 0 {
		return nil
	}
	if p.cancelBulk != nil {
		// A second confirm landing while a prior fanout hasn't
		// drained replaces its context. The prior round's in-flight
		// workers see Done and skip the rest; its own deferred
		// cancel() is then a no-op (idempotent).
		p.cancelBulk()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancelBulk = cancel
	clients := p.clients
	concurrency := p.bulkConcurrency
	logger := p.logger
	bulk := pending.bulk
	ids := pending.ids
	return func() tea.Msg {
		// Local cancel: releases this round's ctx subtree the moment
		// dispatch returns, regardless of whether p.cancelBulk has
		// since been overwritten by a newer round.
		defer cancel()
		successes := dispatchBulkExpire(ctx, clients, ids, concurrency, logger)
		return bulkExpireDoneMsg{
			bulk:      bulk,
			total:     len(ids),
			successes: successes,
		}
	}
}

// handleBulkExpireDone applies a completed bulk-expire fanout.
// Successes drop their marks; everything else (failures and
// unstarted-due-cancel) keeps its mark so re-pressing `x` retries
// only the unfinished work. The flash summary distinguishes
// all-success / partial / all-fail wording.
//
// Does not touch p.cancelBulk — that field may now point to a
// newer round's cancel func (the user re-fired `x` while this
// fanout was still draining). The Cmd that produced this message
// already deferred its own cancel(), so the local ctx is released
// without the handler having to disambiguate.
func (p *Page) handleBulkExpireDone(m bulkExpireDoneMsg) tea.Cmd {
	for _, id := range m.successes {
		delete(p.marks, id)
	}
	failed := m.total - len(m.successes)
	return p.flashExpireResult(m.bulk, len(m.successes), failed)
}

// expireResult is the per-call outcome the worker pool emits onto
// the shared results channel. Tenant rides along for structured
// log attribution on failure.
type expireResult struct {
	id     string
	tenant string
	err    error
}

// dispatchBulkExpire runs the fanout. Tenants run in parallel
// goroutines; inside each tenant a bounded worker pool of
// `min(concurrency, len(ids))` workers consumes from a per-tenant
// jobs channel. concurrency=1 collapses to fully sequential per
// tenant. The producer goroutine respects ctx.Done so a Close()
// mid-flight stops feeding work; in-flight requests are allowed
// to complete (expire is idempotent on the AM side).
//
// Returns the silence IDs whose ExpireSilence returned nil. The
// caller derives "failed = total - len(successes)"; that bucket
// includes both real errors and unstarted-due-cancel. Both keep
// their marks so the user can retry only the unfinished work
// with one more keystroke.
func dispatchBulkExpire(
	ctx context.Context,
	clients map[string]Client,
	ids []pendingExpireID,
	concurrency int,
	logger *slog.Logger,
) []string {
	byTenant := map[string][]string{}
	tenants := []string{}
	for _, id := range ids {
		if _, seen := byTenant[id.tenant]; !seen {
			tenants = append(tenants, id.tenant)
		}
		byTenant[id.tenant] = append(byTenant[id.tenant], id.id)
	}
	resCh := make(chan expireResult, len(ids))
	var tenantWg sync.WaitGroup
	for _, tenant := range tenants {
		client, ok := clients[tenant]
		group := byTenant[tenant]
		if !ok {
			// No client for this tenant — record every queued ID as
			// a failure so the summary count adds up. The mark stays
			// because the result IDs aren't in `successes`.
			for _, id := range group {
				resCh <- expireResult{id: id, tenant: tenant, err: errors.New("no writeable backend for tenant")}
			}
			continue
		}
		tenantWg.Add(1)
		go func(tenant string, ids []string, c Client) {
			defer tenantWg.Done()
			runTenantExpirePool(ctx, tenant, ids, c, concurrency, resCh)
		}(tenant, group, client)
	}
	go func() {
		tenantWg.Wait()
		close(resCh)
	}()
	successes := make([]string, 0, len(ids))
	for r := range resCh {
		if r.err == nil {
			successes = append(successes, r.id)
			continue
		}
		if logger != nil {
			logger.Error("bulk expire: silence expire failed",
				slog.String("backend", r.tenant),
				slog.String("tenant", r.tenant),
				slog.String("silence_id", r.id),
				slog.String("err", r.err.Error()),
			)
		}
	}
	return successes
}

// runTenantExpirePool runs the bounded worker pool for one
// tenant. Producer feeds the jobs channel under ctx.Done so a
// cancellation stops dispatching new work; consumers run
// ExpireSilence and emit results regardless of the ctx state for
// jobs they've already pulled, so an in-flight request completes
// naturally. Workers cap at min(concurrency, len(ids)).
func runTenantExpirePool(
	ctx context.Context,
	tenant string,
	ids []string,
	client Client,
	concurrency int,
	resCh chan<- expireResult,
) {
	workers := max(min(concurrency, len(ids)), 1)
	jobs := make(chan string)
	go func() {
		defer close(jobs)
		for _, id := range ids {
			select {
			case <-ctx.Done():
				return
			case jobs <- id:
			}
		}
	}()
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for id := range jobs {
				err := client.ExpireSilence(ctx, id)
				resCh <- expireResult{id: id, tenant: tenant, err: err}
			}
		})
	}
	wg.Wait()
}

// openEditorForCursor marshals the cursor silence as YAML and
// hands it off to $EDITOR via the injected Resolver. Empty view
// or zero-value Resolver flash a hint instead. Pending state
// (id + tenant) is captured at open time so a poll-tick that
// reorders rows between open and save still routes the update
// to the right backend.
func (p *Page) openEditorForCursor() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.editor.EditorEnv) == 0 && p.editor.DefaultEditor == "" {
		return flashFn(footer.FlashWarn, "editor handoff requires $EDITOR or $A10R_EDITOR")
	}
	entry := p.view[p.cursor]
	if _, ok := p.clients[entry.tenant]; !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	body, err := silenceToYAML(entry.s)
	if err != nil {
		return flashFn(footer.FlashError, "yaml encode: "+err.Error())
	}
	p.pendingEdit = pendingEdit{id: entry.s.ID, tenant: entry.tenant}
	return p.editor.Edit(edit.Request{
		ResourceID: entry.s.ID,
		Initial:    string(body),
		Extension:  "yaml",
	})
}

// handleEditorFinished consumes a FinishedMsg arriving after an
// $EDITOR session. Three branches:
//   - Err set: flash and clear pending state. The silence stays
//     unchanged.
//   - Empty / unchanged content: silent no-op (the user :q'd
//     without writing).
//   - Otherwise: parse YAML, call UpdateSilence on the pending
//     tenant's client, flash success / error.
func (p *Page) handleEditorFinished(m edit.FinishedMsg) tea.Cmd {
	pending := p.pendingEdit
	p.pendingEdit = pendingEdit{}
	if m.Err != nil {
		return flashFn(footer.FlashError, "editor: "+m.Err.Error())
	}
	if strings.TrimSpace(m.Content) == "" {
		return nil
	}
	id, spec, err := silenceFromYAML([]byte(m.Content))
	if err != nil {
		return flashFn(footer.FlashError, "yaml: "+err.Error())
	}
	tenant := pending.tenant
	if tenant == "" {
		// Defensive — pending was cleared between open and finish
		// (concurrent close, etc.). Look up the silence's tenant
		// from the current view via the parsed ID.
		for _, e := range p.view {
			if e.s.ID == id {
				tenant = e.tenant
				break
			}
		}
	}
	client, ok := p.clients[tenant]
	if !ok {
		return flashFn(footer.FlashError, "no writeable backend for silence "+id)
	}
	if err := client.UpdateSilence(context.Background(), id, spec); err != nil {
		return flashFn(footer.FlashError, "update: "+err.Error())
	}
	return flashFn(footer.FlashSuccess, "silence updated: "+id)
}

// flashExpireResult formats the success / partial / total-failure
// flash text for a completed expire round. Branches on the total
// count rather than the bulk flag so a one-mark bulk path reads
// "silence expired" (matching the confirm modal's single-row
// wording) instead of the awkward "expired 1 silences". The
// bulk parameter is retained for symmetry with the message
// envelope but no longer drives wording.
func (p *Page) flashExpireResult(_ bool, success, failed int) tea.Cmd {
	total := success + failed
	if total == 1 {
		if success == 1 {
			return flashFn(footer.FlashSuccess, "silence expired")
		}
		return flashFn(footer.FlashError, "expire failed")
	}
	if failed == 0 {
		return flashFn(footer.FlashSuccess, fmt.Sprintf("expired %d silences", success))
	}
	if success == 0 {
		return flashFn(footer.FlashError, fmt.Sprintf("expire failed for %d silences", failed))
	}
	return flashFn(footer.FlashWarn, fmt.Sprintf("expired %d of %d — %d failed", success, total, failed))
}

// openNewSilenceForm pushes an empty silence form targeting the
// best-fit backend. Selection rule: the cursor row's tenant
// (when a row is focused), else the first in-scope tenant from
// p.clients in alphabetical order. Empty p.clients (no backends
// configured, or read-only run) flashes a hint instead.
func (p *Page) openNewSilenceForm() tea.Cmd {
	tenant, client, ok := p.pickWriteTarget()
	if !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	creator := p.defaultCreator()
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

// defaultCreator returns the page's configured creator, falling back
// to "a10r" when nothing is configured. Shared by the `n` and Ctrl+N
// openers so the fallback rule lives in one place. The `e` opener
// has its own logic (it prefers the source silence's CreatedBy)
// and intentionally does not route through here.
func (p *Page) defaultCreator() string {
	if p.creator != "" {
		return p.creator
	}
	return "a10r"
}

// pickWriteTarget returns the tenant + client to send a write to.
// Cursor row's tenant wins when a row is focused; otherwise falls
// back to the first in-scope tenant (alphabetical for stability).
// Returns (_, _, false) when nothing usable is configured.
func (p *Page) pickWriteTarget() (string, Client, bool) {
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

// emptyState picks the right body for an empty list. The cold-
// start / refresh-in-flight loading hint now lives in the title
// (Title swaps to "<spinner> loading silences…" while !polled
// or refreshing), so the body stays empty in that window — no
// duplicate spinner. After the first DataMsg lands and there's
// genuinely nothing to show, the body explains why: "no silences
// (yet)" for an empty backend, "no silences in view" when a
// filter is the cause.
func (p *Page) emptyState() string {
	if !p.polled() || p.refreshing {
		return ""
	}
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
// Sort markers ride only on the four sortable columns (BY,
// STARTS, ENDS, STATE) — UUID and COMMENT are display-only.
//
// The leading whitespace mirrors the per-row prefix so column
// titles line up with their data: always two cols for the cursor
// slot ("▸ " / "  "), plus another two for the mark glyph
// ("✓ " / "  ") when any row is marked.
func (p *Page) renderHeader(width int) string {
	type col struct {
		label   string
		sortKey string // "" when the column is display-only (UUID, COMMENT, TENANT)
	}
	cols := make([]col, 0, 7)
	if p.showTenantColumn() {
		cols = append(cols, col{label: "TENANT"})
	}
	cols = append(cols,
		col{label: "UUID"},
		col{label: "BY", sortKey: sortKeyCreatedBy},
		col{label: "COMMENT"},
		col{label: "STARTS", sortKey: sortKeyStartsAt},
		col{label: "ENDS", sortKey: sortKeyEndsAt},
		col{label: "STATE", sortKey: sortKeyState},
	)
	// fg-only so the header keeps the terminal default background
	// — painted palette bg in the unstyled body frame creates a
	// coloured stripe.
	headerFg := theme.FgOnly(p.styles.Table.Header.GetForeground())
	activeFg := theme.FgOnly(p.styles.Table.HeaderActive.GetForeground())
	parts := make([]string, len(cols))
	for i, c := range cols {
		label := c.label
		if c.sortKey != "" {
			if arrow := p.sorter.ArrowFor(c.sortKey); arrow != "" {
				label = label + " " + arrow
			}
		}
		// Active sort column gets HeaderActive; everything else
		// (sortable-but-inactive plus display-only) gets the regular
		// Header foreground — both cues (column tint + arrow) point
		// at the same axis.
		if c.sortKey != "" && p.sorter.IsActive(c.sortKey) {
			parts[i] = activeFg.Render(label)
		} else {
			parts[i] = headerFg.Render(label)
		}
	}
	leading := "  "
	if p.hasMarks() {
		leading = "    "
	}
	return leading + p.padColumns(parts, width)
}

// hasMarks reports whether any silence ID is currently marked.
// Inlined-style helper so the renderer can branch without
// poking at p.marks length in two places.
func (p *Page) hasMarks() bool { return len(p.marks) > 0 }

func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, maxRows, len(p.view))
	end := min(p.topRow+maxRows, len(p.view))
	showMark := p.hasMarks()
	var b strings.Builder
	for i := p.topRow; i < end; i++ {
		e := p.view[i]
		row := make([]string, 0, 7)
		if p.showTenantColumn() {
			row = append(row, e.tenant)
		}
		row = append(row,
			clipSilenceID(e.s.ID),
			e.s.CreatedBy,
			singleLine(e.s.Comment),
			p.formatTime(e.s.StartsAt),
			p.formatTime(e.s.EndsAt),
			string(e.s.State),
		)
		prefix := "  "
		if i == p.cursor {
			prefix = "▸ "
		}
		_, marked := p.marks[e.s.ID]
		mark := ""
		if showMark {
			if marked {
				mark = "✓ "
			} else {
				mark = "  "
			}
		}
		// Pad to the full width before styling so the Cursor row's
		// background extends across the whole line k9s-style.
		// Precedence: cursor > marked > expired-dim. Cursor wraps
		// the whole row in fg+bg (the salient "you are here"
		// signal); Marked and the expired-dim treatment both change
		// the foreground only so the row keeps the body's default
		// background — k9s "tinted text" rather than two competing
		// highlighted stripes stacked on top of each other. Dimming
		// fires when the silence is expired (state == expired) and
		// is neither cursor nor marked — same treatment the alerts
		// page applies to suppressed alerts. Marked beats the dim:
		// marked is an explicit user action, expiry is ambient
		// state.
		line := padRight(prefix+mark+p.padColumns(row, width), width)
		switch {
		case i == p.cursor:
			// k9s parity: cursor bg tracks the silence-state
			// colour (active/pending/expired) rather than the
			// static cursorBgColor — see select_table.go:128 in
			// k9s for the equivalent runtime override.
			rowColor := silenceStateColor(e.s.State, p.styles)
			line = p.styles.Table.CursorOver(rowColor).Render(line)
		case marked:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		case e.s.State == backend.SilenceStateExpired:
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

// padColumns lays out a row across fixed-width columns. UUID,
// BY, STARTS, ENDS, and STATE are fixed; COMMENT takes the
// remaining flex so a long Silence.Comment gets the full
// breathing room instead of competing with another text column.
// STARTS / ENDS widen in absolute time mode so the ISO local
// timestamp fits without truncation per Q7.4.
func (p *Page) padColumns(parts []string, width int) string {
	const (
		tenantW = 16
		uuidW   = 10
		byW     = 16 // fits typical 14-char human user names with a 2-col gap
		stateW  = 12
		minDesc = 12
	)
	startsW, endsW := 14, 14
	if p.timeFormat == app.TimeFormatAbsolute {
		startsW, endsW = 20, 20
	}
	fixed := uuidW + byW + startsW + endsW + stateW
	cols := make([]int, 0, 7)
	if p.showTenantColumn() {
		cols = append(cols, tenantW)
		fixed += tenantW
	}
	descW := max(width-fixed, minDesc)
	cols = append(cols, uuidW, byW, descW, startsW, endsW, stateW)
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padCell(v, cols[i]))
	}
	return b.String()
}

// padCell pads s to exactly w display cols and guarantees at
// least one trailing whitespace col so adjacent cells never
// visually merge. Content that meets or exceeds the budget is
// clipped to w-2 with an ellipsis + space appended so the user
// sees both that the cell was truncated and where it ends. The
// gap rule is what fixes `juliette.oraincreated…` and
// `…sys_id=02e61a8619h ago` overlap reports.
func padCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := lipgloss.Width(s)
	if sw <= w-1 {
		return s + strings.Repeat(" ", w-sw)
	}
	if w == 1 {
		return " "
	}
	return truncate(s, w-2) + "… "
}

// formatTime renders ts according to the page's active time
// format. Mirrors the alerts / alert-detail formatters.
func (p *Page) formatTime(ts time.Time) string {
	if p.timeFormat == app.TimeFormatAbsolute {
		return header.FormatAbsolute(ts)
	}
	return header.FormatAge(p.now(), ts)
}

// clipSilenceID returns the leading 8 chars of id so the UUID
// column stays compact. Full IDs remain searchable through the
// filter prompt — silenceMatches scans the unclipped value, so
// an operator can paste any prefix they remember and still find
// the row.
func clipSilenceID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// singleLine flattens any newline / carriage-return / tab inside
// s into a regular space so a multi-line Silence.Comment doesn't
// break the table row alignment. Operators routinely paste URLs
// or runbook excerpts on their own line; without this the COMMENT
// cell's embedded \n shoves STARTS / ENDS / STATE onto the next
// physical line, mid-URL.
func singleLine(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
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
	p.sorter.Apply(p.view)
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
// caller must already be lowercased. ID is included so a UUID
// prefix typed into the filter prompt finds the row whose UUID
// column is clipped to 8 chars.
func silenceMatches(s backend.Silence, q string) bool {
	if strings.Contains(strings.ToLower(s.ID), q) ||
		strings.Contains(strings.ToLower(s.CreatedBy), q) ||
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


// flashFn returns a Cmd that emits a FlashShowMsg with the
// supplied level and text. Tiny indirection so the page's
// action handlers stay one-liners and so the level (Info / Warn
// / Error / Success) reads at the call site.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

// silenceStateColor returns the foreground color associated with a
// silence's state. Used to drive the cursor row's bg per the k9s
// pattern where the selected row's bg tracks the row's semantic
// colour rather than a static cursorBgColor.
func silenceStateColor(s backend.SilenceState, styles theme.Styles) color.Color {
	switch s {
	case backend.SilenceStateActive:
		return styles.SilenceState.Active.GetForeground()
	case backend.SilenceStatePending:
		return styles.SilenceState.Pending.GetForeground()
	case backend.SilenceStateExpired:
		return styles.SilenceState.Expired.GetForeground()
	}
	return styles.SilenceState.Active.GetForeground()
}

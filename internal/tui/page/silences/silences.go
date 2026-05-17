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
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
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
			Less:        func(a, b *silenceEntry) bool { return a.s.CreatedBy < b.s.CreatedBy },
		},
		{
			Key: sortKeyStartsAt, Title: "STARTS", Hotkey: 'S', DefaultAsc: true,
			Description: "sort by startsAt",
			Less:        func(a, b *silenceEntry) bool { return a.s.StartsAt.Before(b.s.StartsAt) },
		},
		{
			Key: sortKeyEndsAt, Title: "ENDS", Hotkey: 'E', DefaultAsc: true,
			Description: "sort by endsAt",
			Less:        func(a, b *silenceEntry) bool { return a.s.EndsAt.Before(b.s.EndsAt) },
		},
		{
			Key: sortKeyState, Title: "STATE", Hotkey: 'T', DefaultAsc: true,
			Less: func(a, b *silenceEntry) bool { return a.s.State < b.s.State },
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
	// lowerComposite is the lower-cased concatenation of every field
	// silenceMatches would otherwise lower-case on every filter
	// keystroke (ID, CreatedBy, Comment, State, plus matchers).
	// Built once at recompute so the filter inner loop becomes a
	// single strings.Contains.
	lowerComposite string
}

// Page is the silences list view.
type Page struct {
	listpage.Base

	styles *theme.Styles
	now    func() time.Time

	// byTenant holds the most recent snapshot for each backend
	// keyed by the poll.DataMsg.Tenant tag. Pages built in single-
	// backend setups end up with one entry; multi-backend ones
	// accumulate every snapshot they've received.
	byTenant map[string][]backend.Silence
	view     []silenceEntry

	// sorter owns the active sort column + direction. Comparators
	// and column metadata come from silenceSortColumns; the helper
	// applies the cycle / flip / walk convention.
	sorter  *tablesort.Sorter[silenceEntry]
	focusID string

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

	// mu guards cancelEditorUpdate. The editor-driven UpdateSilence
	// goroutine sets/clears the cancel while Close() (running on the
	// bubbletea Update goroutine) reads it.
	mu sync.Mutex
	// cancelEditorUpdate cancels the in-flight editor-driven
	// UpdateSilence call. Populated by handleEditorFinished when the
	// async write Cmd is built; cleared by the goroutine's defer.
	// Close() calls it so a page-pop while a slow tenant is writing
	// aborts the request instead of letting the goroutine survive
	// until app shutdown. Mirrors the silence-form (7b8aa88) and
	// tenantconfig (adca17d) per-write cancel pattern.
	cancelEditorUpdate context.CancelFunc

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
	// pausedRefresh, when true, signals "the next DataMsg is from
	// an explicit r-press; honour it even though paused". Cleared
	// after the first DataMsg consumes it. Lets the operator hold
	// pause but pull a single fresh snapshot on demand.
	pausedRefresh bool
	// spinner is the cold-start / refresh-in-flight indicator
	// (bubbles `Points` — three dots cycling). Stopped (i.e. its
	// Tick chain is broken) outside of those two windows; see
	// the spinner.TickMsg branch in Update.
	spinner spinner.Model

	// readOnly mirrors Options.ReadOnly. Bindings() filters
	// Dangerous entries when set so the hint strip and help
	// overlay drop them; handleAction also flashes a hint instead
	// of dispatching the write so a stray keystroke is harmless.
	readOnly bool

	// editorCtx is the parent context the editor subprocess
	// inherits when the user presses Ctrl+E. Wired to the
	// program's RunE ctx so a parent shutdown aborts a hung
	// editor session (audit F16). nil falls back to
	// context.Background() inside edit.Edit.
	editorCtx context.Context //nolint:containedctx // editor subprocess ctx, not session state.
	// bulkCtx is the parent context the bulk-expire fanout
	// inherits. Wired to the program's RunE ctx so a quit
	// cancels the in-flight workers instead of orphaning their
	// goroutines for a multi-day session. nil falls back to
	// context.Background() (preserves the pre-fix behaviour for
	// callers that haven't plumbed it yet).
	bulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.

	// submitCtx parents the silence form's submit ctx. See
	// Options.SubmitCtx for the rationale.
	submitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
}

type Options struct {
	Styles  *theme.Styles
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
	// ReadOnly hides the page's Dangerous bindings from the hint
	// strip / help overlay and turns every write keystroke into a
	// flash hint instead of pushing the form / confirm modal. Wired
	// from the resolved defaults.read_only / --read-only / A10R_READ_ONLY
	// chain so a misclick or stray paste cannot mutate state.
	ReadOnly bool
	// EditorCtx is the parent ctx the Ctrl+E editor subprocess
	// inherits. Cancelling kills the editor — audit F16. nil
	// falls back to context.Background() inside edit.Edit.
	EditorCtx context.Context //nolint:containedctx // editor subprocess ctx, plumbed once at construction.
	// BulkCtx is the parent ctx the bulk-expire fanout inherits.
	// Cancelling cancels every in-flight worker — important for
	// multi-day sessions where a quit must not orphan goroutines.
	// nil falls back to context.Background().
	BulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.
	// SubmitCtx is the parent ctx the silence form's submit ctx
	// derives from. Plumbed through to silenceform.Options.SubmitCtx
	// so an app-level shutdown propagates through the form's
	// in-flight Create/UpdateSilence write — not only through the
	// page-pop / Close cascade. nil falls back to
	// context.Background() inside the form.
	SubmitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
	// Tenants is the canonical list of configured backend names.
	// Drives the TENANT-column visibility decision so a broken
	// tenant (cold-start connection refused, never replies) still
	// counts toward "is this a multi-tenant fleet?". Empty falls
	// back to inferring the count from observed DataMsgs.
	Tenants []string
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
		Base: listpage.Base{
			Scope:      scopeAll,
			LastErrors: map[string]string{},
			Tenants:    opts.Tenants,
		},
		styles:          opts.Styles,
		now:             now,
		clients:         opts.Clients,
		creator:         opts.Creator,
		editor:          opts.EditorResolver,
		timeFormat:      opts.TimeFormat,
		byTenant:        map[string][]backend.Silence{},
		marks:           map[string]struct{}{},
		sorter:          tablesort.New(silenceSortColumns(), sortKeyEndsAt),
		nextRefresh:     map[string]time.Time{},
		polledTenants:   map[string]struct{}{},
		spinner:         sp,
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
		readOnly:        opts.ReadOnly,
		editorCtx:       opts.EditorCtx,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
	}
}

// scopeAll is the canonical "every configured tenant" label.
// Pinned as a constant so the wiring layer and the page stay in
// lockstep — same shape as the alerts page.
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
// side so completing them is safe. Also cancels any in-flight
// editor-driven UpdateSilence so a page-pop mid-write aborts the
// request instead of letting the goroutine outlive the page —
// mirror of the silence-form (7b8aa88) / tenantconfig (adca17d)
// per-write cancel contract.
func (p *Page) Close() tea.Cmd {
	p.mu.Lock()
	cancelEdit := p.cancelEditorUpdate
	p.mu.Unlock()
	if cancelEdit != nil {
		cancelEdit()
	}
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
	scope := p.Scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalSilences()
	if p.Filter != "" {
		return fmt.Sprintf("silences(%s)[%d/%d]", scope, len(p.view), total)
	}
	return fmt.Sprintf("silences(%s)[%d]", scope, total)
}

func (p *Page) HeaderContent() string {
	var parts []string
	if p.Filter != "" {
		parts = append(parts, "filter:"+p.Filter)
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
	if p.Paused {
		// Paused state takes precedence over the refresh countdown
		// so the operator immediately sees that auto-poll is off.
		// The refreshing indicator is kept too — a pausedRefresh
		// in flight is still informative.
		if p.refreshing {
			return "WATCH OFF · refreshing…"
		}
		return "WATCH OFF"
	}
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
	for tenant, detail := range p.LastErrors {
		if detail == "" {
			continue
		}
		if !p.ScopeIncludes(tenant) {
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
		if p.Scope == scopeAll || strings.Contains(p.Scope, ",") {
			return bad[0].tenant + ": " + bad[0].detail
		}
		return bad[0].detail
	}
	return fmt.Sprintf("%d backends erroring; %s: %s", len(bad), bad[0].tenant, bad[0].detail)
}

// soonestNextRefresh returns the earliest DataMsg.NextAt across
// in-scope tenants — the next moment fresh data will land. Zero
// when no in-scope tenant has published a NextAt.
func (p *Page) soonestNextRefresh() time.Time {
	var soonest time.Time
	for tenant, ts := range p.nextRefresh {
		if !p.ScopeIncludes(tenant) {
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
//
// When the page is in read-only mode, Dangerous entries are
// stripped before the slice is returned so the hint strip and
// help overlay both render the read-only verb set without the
// caller having to re-filter.
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
	out = append(out, action.Action{Key: "w", Description: "toggle watch (pause poll)", View: "silences"})
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

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
		if p.ScopeIncludes(tenant) {
			return true
		}
	}
	return false
}

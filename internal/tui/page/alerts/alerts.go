// SPDX-License-Identifier: Apache-2.0

// Package alerts renders the alerts list page — the home view of
// the TUI. v0.1 ships a minimal table:
//
//   - Vim motions (j/k/g/G/Ctrl+D/Ctrl+U/Ctrl+F/Ctrl+B) plus arrow keys.
//   - Substring filter via the `/` prompt (App routes
//     PromptSubmittedMsg{PromptFilter} to the page).
//   - Severity / alertname / instance / age columns.
//   - Sort cycling by `Shift+S` (severity), `Shift+N` (alertname),
//     `Shift+T` (state), `Shift+R` (receiver). `h`/`l` walk
//     between sort columns.
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
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Sort column keys. Stable identifiers passed to the tablesort
// helper — used as both the canonical Key (for ArrowFor / IsActive
// lookups) and the lower-cased description text in the help
// overlay (the helper derives "sort by <title>" from each Column's
// Title, which lower-cases to these strings). Order matches the
// cycle order for the h/l sort-column walk.
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
			Less: func(a, b *alertEntry) bool {
				return backend.SeverityRank(a.a.Labels) < backend.SeverityRank(b.a.Labels)
			},
		},
		{
			Key: sortKeyName, Title: "ALERTNAME", Hotkey: 'N', DefaultAsc: true,
			Less: func(a, b *alertEntry) bool {
				return a.a.Labels["alertname"] < b.a.Labels["alertname"]
			},
		},
		{
			Key: sortKeyState, Title: "STATE", Hotkey: 'T', DefaultAsc: true,
			Less: func(a, b *alertEntry) bool { return a.a.State < b.a.State },
		},
		{
			Key: sortKeyAge, Title: "AGE", Hotkey: 'A', DefaultAsc: true,
			Less: func(a, b *alertEntry) bool { return a.a.StartsAt.Before(b.a.StartsAt) },
		},
	}
}

// Options bundles the per-page constructor inputs.
type Options struct {
	Styles *theme.Styles
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
	// value (timerender.Relative) is the pre-toggle default.
	TimeFormat timerender.Format
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
	// ReadOnly hides the page's Dangerous bindings (`s` for
	// silence) from the hint strip / help overlay and turns the
	// keystroke into a flash hint. Wired from the resolved
	// defaults.read_only chain — see internal/config/resolve.go.
	ReadOnly bool
	// BulkCtx is the parent ctx the bulk-silence fanout inherits.
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
	// InitialStateFilter pre-seeds the `t` cycle's state filter so a
	// `:alerts --state suppressed` (typed at the prompt or via a user
	// alias's expansion) lands on the suppressed-only view. Empty
	// leaves the filter unset (page default — all states). Invalid
	// values are rejected by the cmdbar wiring before the page is
	// constructed; this field trusts its inputs.
	InitialStateFilter string
	// InitialFilter pre-seeds the `/` substring filter so a user alias
	// can land the page on a search subset without an extra keystroke.
	// Empty leaves the filter unset.
	InitialFilter string
	// Tenants is the canonical list of configured backend names — what
	// the wiring layer parses from cfg.Backends. The page uses it to
	// decide whether to render the leading TENANT column: a fleet of
	// ≥2 configured backends shows the column for the "all" scope
	// regardless of which tenants have actually produced data yet.
	// Without this, a tenant that never replies (cold-start connection
	// refused, slow first tick) would silently disappear from the view
	// because the count of *known* tenants stays at 1. Empty falls
	// back to the legacy behaviour of inferring tenant count from
	// observed DataMsgs — kept for tests that don't care about the
	// column toggle.
	Tenants []string
}

// alertEntry pairs an alert with the tenant tag the poller
// emitted it under so the table can surface a TENANT column when
// the active scope spans multiple backends.
type alertEntry struct {
	a      backend.Alert
	tenant string
	// lowerComposite is the lower-cased concatenation of every
	// label and annotation value the filter would otherwise lower-
	// case on every keystroke. Built once at recompute so the
	// filter inner loop is a single strings.Contains.
	lowerComposite string
}

// Page is the alerts list view. Implements app.Page.
type Page struct {
	listpage.Base
	listpage.PollingUI

	styles *theme.Styles
	now    func() time.Time

	// clients is the per-tenant write surface for `s`; see Options.
	clients map[string]silenceform.Client
	// creator seeds the silence form's CreatedBy field.
	creator string

	// byTenant stores the most recent snapshot per tenant. Each
	// poller emits a DataMsg keyed to its own Tenant; recompute
	// unions the snapshots before sorting / filtering.
	byTenant map[string][]backend.Alert

	view []alertEntry // filtered + sorted view (recomputed on change)

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
	timeFormat timerender.Format

	// readOnly mirrors Options.ReadOnly. Bindings() filters
	// Dangerous entries when set; handleAction flashes a hint
	// instead of opening the silence form.
	readOnly bool

	// bulkCtx parents the bulk-silence fanout. See Options.BulkCtx.
	bulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.

	// submitCtx parents the silence form's submit ctx. See
	// Options.SubmitCtx for the rationale.
	submitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
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
	p := &Page{
		Base: listpage.Base{
			Scope:         opts.Scope,
			Filter:        opts.InitialFilter,
			BackendHealth: map[string]listpage.BackendHealth{},
			Tenants:       opts.Tenants,
		},
		PollingUI: listpage.PollingUI{
			PolledTenants: map[string]struct{}{},
			NextRefresh:   map[string]time.Time{},
			Spinner:       sp,
		},
		styles:          opts.Styles,
		now:             now,
		clients:         opts.Clients,
		creator:         opts.Creator,
		timeFormat:      opts.TimeFormat,
		byTenant:        map[string][]backend.Alert{},
		sorter:          tablesort.New(alertSortColumns(), sortKeySeverity),
		marks:           map[string]struct{}{},
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
		readOnly:        opts.ReadOnly,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
		stateFilter:     opts.InitialStateFilter,
	}
	p.Recompute = p.recompute
	p.RowCount = func() int { return len(p.view) }
	p.SnapshotFocus = p.snapshotFocus
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	p.ClearMarks = p.handleClearMarks
	return p
}

// SetScope updates the active tenant scope and rebuilds the
// view so the title's `[N]` count and the rendered rows both
// reflect the new selection. Mirror of the app.ScopeChangedMsg
// handler — exists for direct callers (the cmd-bar wiring,
// tests) that don't go through bubbletea's message bus.
func (p *Page) SetScope(s string) {
	p.Scope = s
	p.recompute()
}

// Init implements app.Page. Kicks the spinner so the cold-start
// "loading" affordance animates while the first poll tick lands.
// The Tick chain is broken in Update once the page has any
// in-scope DataMsg (and re-armed on each manual `r` refresh) so
// the spinner stops costing per-frame redraws when there's
// nothing to wait for.
func (p *Page) Init() tea.Cmd { return p.Spinner.Tick }

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
	if !p.polled() || p.Refreshing {
		return p.Spinner.View() + " loading alerts…"
	}
	scope := p.Scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalAlerts()
	if p.Filter != "" || p.stateFilter != "" {
		return fmt.Sprintf("alerts(%s)[%d/%d]", scope, len(p.view), total)
	}
	return fmt.Sprintf("alerts(%s)[%d]", scope, total)
}

func (p *Page) HeaderContent() string {
	var parts []string
	if p.Filter != "" {
		parts = append(parts, "filter:"+p.Filter)
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
	if p.Paused {
		// Paused state takes precedence over the refresh countdown
		// so the operator immediately sees that auto-poll is off.
		// The refreshing indicator is kept too — a pausedRefresh
		// in flight is still informative.
		if p.Refreshing {
			return "WATCH OFF · refreshing…"
		}
		return "WATCH OFF"
	}
	if p.Refreshing {
		return "refreshing…"
	}
	if !p.polled() {
		return ""
	}
	next := p.soonestNextRefresh()
	if next.IsZero() {
		return ""
	}
	return "next refresh " + listpage.NextRefreshLabel(p.now(), next)
}

// soonestNextRefresh returns the earliest DataMsg.NextAt across
// in-scope tenants. Zero when no in-scope tenant has published a
// NextAt — the wiring layer's poll.DataMsg is the single source.
func (p *Page) soonestNextRefresh() time.Time {
	var soonest time.Time
	for tenant, ts := range p.NextRefresh {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		if soonest.IsZero() || ts.Before(soonest) {
			soonest = ts
		}
	}
	return soonest
}

// polled reports whether at least one in-scope tenant has
// produced a DataMsg. Read by Title / Footer / emptyState to
// pick between the loading affordance and the populated frame.
// Scope-aware so a fast out-of-scope tenant in a multi-backend
// setup doesn't flip the page out of loading state before the
// in-scope tenant has answered.
func (p *Page) polled() bool {
	for tenant := range p.PolledTenants {
		if p.ScopeIncludes(tenant) {
			return true
		}
	}
	return false
}

// spinnerActive reports whether the spinner should continue to
// animate. Two windows: cold start and refresh-in-flight.
// Outside those, the page draws static "next refresh" timing.
func (p *Page) spinnerActive() bool { return !p.polled() || p.Refreshing }

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
//
// When the page is in read-only mode the Dangerous entries (`s`)
// are stripped before the slice is returned so the hint strip and
// help overlay both render the read-only verb set.
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
	out = append(out,
		action.Action{Key: "r", Description: "refresh", View: "alerts"},
		action.Action{Key: "w", Description: "toggle watch (pause poll)", View: "alerts"},
	)
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

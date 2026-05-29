// SPDX-License-Identifier: Apache-2.0

// Package alerts renders the alerts list page — the home view of
// the TUI. It rows on the alertname aggregate (one row per
// (tenant, alertname)), not per instance: every backend.Alert
// sharing an alertname rolls up into one alertGroup carrying a
// COUNT, a per-state breakdown, the max severity, and the oldest
// age. See CONTEXT.md "Alert aggregation" and ADR 0038.
//
//   - Vim motions (j/k/g/G/Ctrl+D/Ctrl+U/Ctrl+F/Ctrl+B) plus arrow keys.
//   - Substring filter via the `/` prompt (App routes
//     PromptSubmittedMsg{PromptFilter} to the page). The filter
//     narrows instances first; groups then rebuild from survivors.
//   - Severity / alertname / count / state / age columns (plus
//     tenant when the active scope spans more than one backend).
//   - Sort cycling by `Shift+S` (severity), `Shift+N` (alertname),
//     `Shift+C` (count), `Shift+A` (age). `h`/`l` walk between
//     sort columns.
//   - Enter drills: a COUNT==1 group skips the L2 group-detail page
//     straight to the single-instance L3 detail; a COUNT>1 group
//     pushes the L2 group-detail instance list.
//   - `s` is silence-all: with no marks it prefills `alertname=<X>`
//     for the cursor group (gated by a confirm modal when COUNT>1,
//     blast radius not mark count); with marks it fans out one
//     alertname silence per marked group. Read-only mode hides the
//     binding via the action registry.
//
// Polling lives in the wiring layer (cmd/tui.go): a poll loop
// emits DataMsg{Resource: []backend.Alert} that this page
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
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
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
	sortKeyCount    = "count"
	sortKeyAge      = "age"
)

// scopeAll is the canonical label for the "every configured
// tenant" scope. Used by Title, scopeIncludes, and the
// `<0>` quick-switch payload — pinning it as a constant keeps
// the wiring layer and the page in lockstep.
const scopeAll = "all"

// alertSortColumns returns the page's sortable column set, now keyed
// on the alertname aggregate. Severity and count default DESC (worst /
// largest first); alertname and age read naturally ascending. Every
// comparator falls back to alertName ASC then tenant ASC so the order
// is total and deterministic across re-sorts / poll ticks regardless
// of ingest order.
//
// Severity uses Hotkey 'S' (Shift+S): unlike the L2 page, alerts L1
// has no uppercase `S` verb — silence is lowercase `s` — so the
// shortcut is free.
func alertSortColumns() []tablesort.Column[alertGroup] {
	return []tablesort.Column[alertGroup]{
		{
			Key: sortKeySeverity, Title: "SEVERITY", Hotkey: 'S', DefaultAsc: false,
			Less: tieBreakGroup(func(a, b *alertGroup) bool {
				return a.severityRank < b.severityRank
			}),
		},
		{
			Key: sortKeyName, Title: "ALERTNAME", Hotkey: 'N', DefaultAsc: true,
			Less: tieBreakGroup(func(a, b *alertGroup) bool {
				return a.alertName < b.alertName
			}),
		},
		{
			Key: sortKeyCount, Title: "COUNT", Hotkey: 'C', DefaultAsc: false,
			Less: tieBreakGroup(func(a, b *alertGroup) bool {
				return a.count < b.count
			}),
		},
		{
			Key: sortKeyAge, Title: "AGE", Hotkey: 'A', DefaultAsc: true,
			Less: tieBreakGroup(func(a, b *alertGroup) bool {
				return a.oldestStart.Before(b.oldestStart)
			}),
		},
	}
}

// tieBreakGroup wraps a comparator so equal-by-primary groups fall
// back to alertName ASC then tenant ASC. Without this, sort.SliceStable
// would keep input order on ties — fine for cursor stickiness but not
// a total order, so identical inputs in a different ingest order would
// render differently. (tenant, alertName) is unique per group, so the
// fallback yields one canonical layout.
func tieBreakGroup(primary func(a, b *alertGroup) bool) func(a, b *alertGroup) bool {
	return func(a, b *alertGroup) bool {
		if primary(a, b) {
			return true
		}
		if primary(b, a) {
			return false
		}
		if a.alertName != b.alertName {
			return a.alertName < b.alertName
		}
		return a.tenant < b.tenant
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
	// StateFormat seeds the STATE-column breakdown density. Zero value
	// (stateformat.Full) is the pre-toggle default, so a zero-value
	// Options opens in the legible full mode — boot wiring threading
	// this through is a separate later commit.
	StateFormat stateformat.Format
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
	// EditorResolver handles the `Ctrl+E` round-trip on the
	// restricted silences page pushed by `S` from the alert-detail
	// page drilled into from this page. Matches silences.Options.EditorResolver.
	EditorResolver edit.Resolver
	// EditorCtx is the parent ctx the editor subprocess and bulk-
	// expire fanout inherit on the restricted silences page pushed
	// by `S` from alert-detail. Matches silences.Options.EditorCtx.
	EditorCtx context.Context //nolint:containedctx // editor subprocess ctx, plumbed once at construction.
	// InitialStateFilter pre-seeds the Shift+F cycle's state filter so a
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
// emitted it under. It survives only as the per-instance unit the
// substring / state filter operates on before aggregation — the
// table rows on alertGroup, not on alertEntry.
type alertEntry struct {
	a      backend.Alert
	tenant string
	// lowerComposite is the lower-cased concatenation of every
	// label and annotation value the filter would otherwise lower-
	// case on every keystroke. Built once at recompute so the
	// filter inner loop is a single strings.Contains.
	lowerComposite string
}

// alertGroup is the alertname aggregate the table rows on — every
// post-filter instance sharing one (tenant, alertname). A TUI-layer
// concept synthesised by recompute; no backend type. See CONTEXT.md
// "Alert aggregation".
type alertGroup struct {
	tenant    string
	alertName string
	// instances are the surviving backend.Alert values for this
	// group, sorted by fingerprint ASC for a stable drill-down order.
	instances []backend.Alert
	count     int
	// severityRank is the MAX backend.SeverityRank across instances —
	// the worst severity headlines the row.
	severityRank int
	// oldestStart is the MIN StartsAt across instances — the AGE cell.
	oldestStart time.Time
	// active / suppressed / unprocessed tally the instances per AM
	// state; they sum to count and feed the STATE breakdown.
	active      int
	suppressed  int
	unprocessed int
}

// key is the group's stable identity — the cursor-focus anchor and
// the mark key. NUL-joined so a tenant or alertname containing the
// separator can't forge another group's key.
func (g alertGroup) key() string { return g.tenant + "\x00" + g.alertName }

// allSuppressed reports whether every instance in the group is
// suppressed — the row-dim condition. A zero-count group is never
// "all suppressed" (there is nothing to dim).
func (g alertGroup) allSuppressed() bool { return g.count > 0 && g.suppressed == g.count }

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

	groups []alertGroup // filtered + aggregated + sorted view (recomputed on change)

	// focusGroupKey is the group the cursor was on before the last
	// recompute. Tracking by group key (not index) keeps the cursor on
	// the same (tenant, alertname) across poll-tick refreshes, sort
	// changes, and filter changes. Empty when no group is focused
	// (cold start, empty view).
	focusGroupKey string

	// marks is the set of group keys the user has Space-toggled for
	// bulk silence-all. Tracking by group key, like the cursor focus,
	// so marks survive re-sorts and re-filters without sliding onto
	// unrelated groups. `s` with marks fans out one alertname=<X>
	// silence per marked group; failed targets keep their marks so the
	// next `s` retries only the unfinished work.
	marks map[string]struct{}

	// pendingBulkSilence captures the resolved bulk-silence targets
	// between an opened confirm modal (N≥2 marks) and its
	// ConfirmResultMsg, or between an opened bulk form (any N≥1)
	// and its BulkSubmittedMsg. Cleared after consumption.
	pendingBulkSilence pendingBulkSilence

	// pendingSilenceAll captures the single-cursor silence-all target
	// (count>1) between its blast-radius confirm modal and the
	// ConfirmResultMsg. DISTINCT from pendingBulkSilence: the
	// single-cursor confirm and the ≥2-marks bulk confirm are separate
	// code paths and must not share state. Cleared after consumption.
	pendingSilenceAll pendingSilenceAll

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
	sorter      *tablesort.Sorter[alertGroup]
	stateFilter string // "" = all, otherwise an AlertState value

	// timeFormat mirrors the app-global toggle. Defaults to
	// relative; flipped by app.TimeFormatChangedMsg so every list
	// page agrees on absolute vs. relative timestamps.
	timeFormat timerender.Format

	// stateFormat mirrors the app-global STATE-breakdown density
	// toggle. Defaults to Full; flipped by app.StateFormatChangedMsg
	// so L1 and the L2 group-detail page agree on density.
	stateFormat stateformat.Format

	// readOnly mirrors Options.ReadOnly. Bindings() filters
	// Dangerous entries when set; handleAction flashes a hint
	// instead of opening the silence form.
	readOnly bool

	// bulkCtx parents the bulk-silence fanout. See Options.BulkCtx.
	bulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.

	// submitCtx parents the silence form's submit ctx. See
	// Options.SubmitCtx for the rationale.
	submitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.

	// editorResolver and editorCtx are forwarded to the alert-detail
	// page so it can pass them to the restricted silences page pushed
	// by `S` when the alert has N>1 silenced-by IDs (ADR 0035).
	editorResolver edit.Resolver
	editorCtx      context.Context //nolint:containedctx // editor subprocess ctx, plumbed once at construction.
}

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
		stateFormat:     opts.StateFormat,
		byTenant:        map[string][]backend.Alert{},
		sorter:          tablesort.New(alertSortColumns(), sortKeySeverity),
		marks:           map[string]struct{}{},
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
		readOnly:        opts.ReadOnly,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
		stateFilter:     opts.InitialStateFilter,
		editorResolver:  opts.EditorResolver,
		editorCtx:       opts.EditorCtx,
	}
	p.Recompute = p.recompute
	p.RowCount = func() int { return len(p.groups) }
	p.SnapshotFocus = p.snapshotFocus
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	p.SetStateFormat = func(f stateformat.Format) { p.stateFormat = f }
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

// Init kicks the spinner so the cold-start "loading" affordance
// animates until the first DataMsg lands. Update breaks the Tick
// chain once any in-scope DataMsg has arrived and re-arms it on
// every manual `r` refresh.
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

func (*Page) Crumb() string { return "alerts" }

// Title is k9s-style "alerts(<scope>)[<count>]". During a loading
// window the title flips to the loading affordance so the border
// itself reads as the loading state.
func (p *Page) Title() string {
	if p.SpinnerActive(p.ScopeIncludes) {
		return p.LoadingTitle("alerts")
	}
	scope := p.Scope
	if scope == "" {
		scope = scopeAll
	}
	if p.Filter != "" || p.stateFilter != "" {
		return fmt.Sprintf("alerts(%s)[%d/%d]", scope, len(p.groups), p.totalGroups())
	}
	return fmt.Sprintf("alerts(%s)[%d]", scope, len(p.groups))
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

// Footer is the refresh countdown surface — see CONTEXT.md.
func (p *Page) Footer() string {
	return listpage.RefreshCountdown(
		p.Paused, p.Refreshing,
		p.PolledInScope(p.ScopeIncludes),
		p.SoonestNextRefresh(p.ScopeIncludes),
		p.now(),
	)
}

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
	out := make([]action.Action, 0, 8+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "detail", View: "alerts"},
		action.Action{Key: "Space", Description: "mark", View: "alerts", Shared: true},
		action.Action{Key: "s", Description: "silence", View: "alerts", Dangerous: true},
		action.Action{Key: "/", Description: "filter", View: "alerts"},
		action.Action{Key: "Shift+F", Description: "state filter", View: "alerts"},
	)
	out = append(out, sortBindings...)
	// `r` is a global binding too; surface it on the alerts hint
	// strip so the affordance reads at a glance alongside the
	// page-specific verbs. Same shape as silences.
	out = append(out,
		action.Action{Key: "Shift+T", Description: "state format", View: "alerts"},
		action.Action{Key: "r", Description: "refresh", View: "alerts"},
		action.Action{Key: "w", Description: "toggle watch", View: "alerts"},
	)
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

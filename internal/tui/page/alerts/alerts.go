// SPDX-License-Identifier: Apache-2.0

// Package alerts renders the alerts list page — the home view of
// the TUI. It rows on the alertname aggregate (one row per
// (tenant, alertname)), not per instance: every backend.Alert
// sharing an alertname rolls up into one alertGroup carrying a
// COUNT, a per-state breakdown, the max severity, and the oldest
// age. See CONTEXT.md "Alert aggregation" and ADR 0040.
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
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/page/table"
	"github.com/wilfriedroset/a10r/internal/tui/session"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

const (
	sortKeySeverity = "severity"
	sortKeyName     = "alertname"
	sortKeyCount    = "count"
	sortKeyAge      = "age"
)

const (
	// resourceAlerts is the page's resource id. wordAlert / wordAlerts
	// are operator-facing prose, so the id must not double as a noun.
	resourceAlerts = "alerts"
	wordAlert      = "alert"
	wordAlerts     = "alerts"
)

// labelAlertname is the Alertmanager wire-format label key. Distinct
// from sortKeyName despite the shared value: one is a TUI sort axis,
// the other a backend label, and renaming the sort key must not
// silently re-target label lookups.
const labelAlertname = "alertname"

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
func alertSortColumns(user []table.LabelColumn) []tablesort.Column[alertGroup] {
	cols := []tablesort.Column[alertGroup]{
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
	}
	// The user block goes between ALERTNAME and COUNT, not after AGE,
	// because the h/l walk steps this slice and it has to match what
	// columns() renders. STATE renders but is not an axis, and a
	// column with no sort_key renders but is skipped below, so the
	// walk is the rendered order minus those two.
	cols = append(cols, labelSortColumns(user)...)
	return append(cols, []tablesort.Column[alertGroup]{
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
	}...)
}

// labelSortColumns turns each user-declared column into a sortable
// axis over the pre-computed labelCells slice. Cells compare
// byte-wise with rollup markers ranked last, and an empty cell is
// pinned to the tail in both directions (ADR 0048).
func labelSortColumns(user []table.LabelColumn) []tablesort.Column[alertGroup] {
	out := make([]tablesort.Column[alertGroup], 0, len(user))
	for _, c := range user {
		idx := c.Index
		// A column with no sort_key is not a sort axis at all, not
		// merely one without a shortcut: tablesort's h/l walk visits
		// zero-hotkey columns, so registering it would make a column
		// the operator declared unsortable the active sort and
		// persist it to the sort-memory file.
		if c.Hotkey == 0 {
			continue
		}
		out = append(out, tablesort.Column[alertGroup]{
			Key: c.Key, Title: c.Title, Hotkey: c.Hotkey, DefaultAsc: true,
			Less: tieBreakGroup(func(a, b *alertGroup) bool {
				return table.CellLess(labelCellAt(a, idx), labelCellAt(b, idx))
			}),
			Tail: func(g *alertGroup) bool { return table.CellEmpty(labelCellAt(g, idx)) },
		})
	}
	return out
}

// isHiddenSortKey reports a sort axis the operator cannot see right
// now, which is a wide column outside the wide tier. Installed on the
// sorter so h/l steps over it and its hotkey stays dead.
func (p *Page) isHiddenSortKey(key string) bool {
	for _, c := range p.labelCols {
		if c.Key == key {
			return c.Wide && !p.wide
		}
	}
	return false
}

// toggleWide flips the display tier. It reports false when the page
// declares no wide column, which spares the caller a recompute that
// would paint an identical frame.
func (p *Page) toggleWide() bool {
	if !table.HasWide(p.labelCols) {
		return false
	}
	p.wide = !p.wide
	p.shownCols = table.Visible(p.labelCols, p.wide)
	return true
}

// labelCellAt reads a group's cell for user column i. aggregate fills
// one cell per column for every group, so the guard is there for
// hand-built alertGroup literals in tests.
func labelCellAt(g *alertGroup, i int) string {
	if i >= len(g.labelCells) {
		return ""
	}
	return g.labelCells[i]
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

type Options struct {
	Styles *theme.Styles
	// Now injects the wall clock for the age column. nil falls
	// back to time.Now.
	Now   func() time.Time
	Scope string
	// Clients is the per-tenant write surface for 's'; nil flashes a hint.
	Clients map[string]silenceform.Client
	// Creator seeds CreatedBy; empty falls back to "a10r".
	Creator string
	// TimeFormat seeds the page's time-format mode at construction
	// so a page pushed *after* the user toggled `t` doesn't open
	// in relative while the rest of the app reads absolute. Zero
	// value (timerender.Relative) is the pre-toggle default.
	TimeFormat timerender.Format
	// StateFormat seeds the STATE-column breakdown density. Zero value
	// (stateformat.Full) is the pre-toggle default, so a zero-value
	// Options opens in the legible full mode.
	StateFormat stateformat.Format
	// Logger receives per-failure detail (`backend`, `tenant`,
	// `alert_fingerprint`, `err`) at error level when the bulk
	// fanout surfaces individual CreateSilence failures. Nil
	// suppresses logging.
	Logger *slog.Logger
	// Session is the live configuration the page reads its write
	// policy and bulk pool size from at the point of use, so a reload
	// reaches the page while it is open. Nil reads as an empty config.
	Session *session.Session
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
	// PollDelta is wired from `tui.poll_delta`. When true, a poll
	// that adds or removes an aggregate flashes the delta. Opt-in,
	// like tui.tips and tui.terminal_title.
	PollDelta bool
	// SortMemory persists the active sort column across runs; nil
	// disables sort memory for this page.
	SortMemory tablesort.Memory
	// Columns are the user-declared label columns from
	// `pages.alerts.columns`. The config loader has validated them;
	// the page renders them in order. Empty leaves the table on its
	// built-in columns alone.
	Columns []config.Column
	// GroupDetailColumns are `pages.group_detail.columns`, carried
	// through rather than read here: the L2 page is constructed on
	// drill-down from this page, so this is the only path the
	// configuration has to reach it.
	GroupDetailColumns []config.Column
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
	// labelCells holds one rolled-up cell per user-declared label
	// column, in config order. Computed once per aggregate so the
	// sort comparators and the row renderer never walk the instance
	// slice again — the render budget is O(rows), not O(rows x
	// instances).
	labelCells []string
}

// key is the group's stable identity — the cursor-focus anchor and
// the mark key. NUL-joined so a tenant or alertname containing the
// separator can't forge another group's key.
func (g alertGroup) key() string { return groupKeyOf(g.tenant, g.alertName) }

// groupKeyOf is the single spelling of the (tenant, alertname)
// identity. Every lookup that has to agree with aggregate's map goes
// through it, NUL-separated so a tenant name cannot forge a key.
func groupKeyOf(tenant, alertName string) string { return tenant + "\x00" + alertName }

// markKey hands the listpage mark and range helpers the group key,
// so a re-sort carries a mark with its row instead of its index.
func markKey(g alertGroup) string { return g.key() }

// silenceRequest asks about the run an `s` press would really fire,
// so the duplicate tenants are the per-tenant count the cap compares
// against.
func (p *Page) silenceRequest() guardrail.Request {
	return p.request(p.markedTargets)
}

// guarded answers the [guarded] suffix. It counts each marked tenant
// once: a binding outlives any one run, so a cap the current marks
// happen to breach must not strike `s` off the hint strip. Bindings()
// runs on the render path and again on every key press, so an
// unconfigured policy pays for no walk at all.
func (p *Page) guarded() bool {
	rules := p.session.Guardrails()
	if len(rules) == 0 {
		return false
	}
	return rules.Decide(p.request(p.markedTenants)).Refused()
}

// request turns the press into its targets; marked resolves the bulk
// fan-out, the one case the callers count differently.
//
// Lead follows the press rather than the rule, so the sentence names
// the key the user pressed. Only the marked fan-out has a name of its
// own; a cursor press reads back as the rule to edit.
func (p *Page) request(marked func() []string) guardrail.Request {
	switch {
	case len(p.marks) > 0:
		return guardrail.Request{Action: guardrail.ActionSilenceCreate, Tenants: marked(), Lead: "bulk silence"}
	case p.Index() < len(p.groups):
		return guardrail.Request{Action: guardrail.ActionSilenceCreate, Tenants: []string{p.groups[p.Index()].tenant}}
	}
	return guardrail.Request{Action: guardrail.ActionSilenceCreate}
}

// markedTargets names the tenant of every marked group, once per group
// and in the page's own row order. It keeps a marked tenant with no
// writeable client, which resolveBulkSilenceTargets drops: refusing a
// press that would have flashed "no writeable backend" costs nothing,
// and aligning the two walks would let a capped or denied tenant
// through whenever its client is missing at that moment.
func (p *Page) markedTargets() []string {
	var out []string
	for _, g := range p.groups {
		if _, marked := p.marks[markKey(g)]; marked {
			out = append(out, g.tenant)
		}
	}
	return out
}

// markedTenants serves the caller that asks per backend rather than
// per row.
func (p *Page) markedTenants() []string {
	var out []string
	for _, g := range p.groups {
		if _, marked := p.marks[markKey(g)]; marked && !slices.Contains(out, g.tenant) {
			out = append(out, g.tenant)
		}
	}
	return out
}

// allSuppressed reports whether every instance in the group is
// suppressed — the row-dim condition. A zero-count group is never
// "all suppressed" (there is nothing to dim).
func (g alertGroup) allSuppressed() bool { return g.count > 0 && g.suppressed == g.count }

// The range-mark contract from listpage.Base reaches the app shell
// only through these two optional interfaces, and neither is named
// anywhere else in the package.
var (
	_ app.EscapeConsumer = (*Page)(nil)
	_ app.Suspender      = (*Page)(nil)
)

// Implements app.Page.
type Page struct {
	listpage.Base
	listpage.PollingUI

	styles *theme.Styles
	now    func() time.Time

	// clients is the per-tenant write surface for `s`; see Options.
	clients map[string]silenceform.Client
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
	// between an opened confirm modal and its ConfirmResultMsg, or
	// between an opened bulk form and its BulkSubmittedMsg. Cleared
	// after consumption.
	pendingBulkSilence pendingBulkSilence

	// pendingSilenceAll captures the single-cursor silence-all target
	// (count>1) between its blast-radius confirm modal and the
	// ConfirmResultMsg. DISTINCT from pendingBulkSilence: the
	// single-cursor confirm and the marked bulk confirm are separate
	// code paths and must not share state. Cleared after consumption.
	pendingSilenceAll pendingSilenceAll

	// logger: nil suppresses logging.
	logger *slog.Logger
	// cancelBulk cancels the in-flight bulk-silence fanout when
	// set. Populated when fanout starts; the dispatch Cmd defers
	// its own cancel() so a stale done arriving after a newer
	// round started cannot abort the newer round (mirrors the
	// silences page's contract).
	cancelBulk context.CancelFunc

	// labelCols are the user-declared label columns, resolved once at
	// construction. shownCols is the subset the current display tier
	// renders, and wide is that tier: false hides every `wide: true`
	// column until the operator presses Shift+W. A row's cells and
	// the sorter's axes stay keyed by labelCols order through
	// table.LabelColumn.Index, so toggling the tier moves no cell.
	labelCols []table.LabelColumn
	shownCols []table.LabelColumn
	wide      bool

	// labelWidths is the measured cell width of each shownCols entry
	// over the whole filtered view, refreshed by recompute so the
	// renderer never re-scans the rows per frame.
	labelWidths []int

	scroll table.Scroll
	// groupDetailCols is the L2 page's column configuration, held
	// only to hand to groupdetail.New on drill-down.
	groupDetailCols []config.Column

	// sorter: comparators from alertSortColumns.
	sorter *tablesort.Sorter[alertGroup]

	// sortMemory is held only to hand down to the L2 group-detail
	// page, which this page constructs and boot never sees.
	sortMemory  tablesort.Memory
	stateFilter string // "" = all, otherwise an AlertState value

	// timeFormat is flipped by app.TimeFormatChangedMsg so all list pages agree.
	timeFormat timerender.Format

	// stateFormat is flipped by app.StateFormatChangedMsg so L1 and L2 agree on density.
	stateFormat stateformat.Format

	session *session.Session

	// pollDelta: see Options.PollDelta.
	pollDelta bool

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
	sp := spinner.New(spinner.WithSpinner(spinner.Points))
	labelCols := table.Resolve(opts.Columns)
	p := &Page{
		Scope:           opts.Scope,
		BackendHealth:   map[string]listpage.BackendHealth{},
		Tenants:         opts.Tenants,
		PolledTenants:   map[string]struct{}{},
		NextRefresh:     map[string]time.Time{},
		Spinner:         sp,
		styles:          opts.Styles,
		now:             now,
		clients:         opts.Clients,
		creator:         opts.Creator,
		timeFormat:      opts.TimeFormat,
		stateFormat:     opts.StateFormat,
		byTenant:        map[string][]backend.Alert{},
		labelCols:       labelCols,
		shownCols:       table.Visible(labelCols, false),
		groupDetailCols: opts.GroupDetailColumns,
		sorter:          tablesort.New(alertSortColumns(labelCols), sortKeySeverity),
		marks:           map[string]struct{}{},
		logger:          opts.Logger,
		session:         session.OrEmpty(opts.Session),
		pollDelta:       opts.PollDelta,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
		stateFilter:     opts.InitialStateFilter,
		editorResolver:  opts.EditorResolver,
		editorCtx:       opts.EditorCtx,
		sortMemory:      opts.SortMemory,
	}
	p.sorter.Bind(opts.SortMemory, resourceAlerts)
	p.sorter.SetHidden(p.isHiddenSortKey)
	p.Recompute = p.recompute
	p.Grammar = filterexpr.AlertGrammar
	p.SetFilter(opts.InitialFilter)
	p.RowCount = func() int { return len(p.groups) }
	p.SnapshotFocus = p.snapshotFocus
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	p.SetStateFormat = func(f stateformat.Format) { p.stateFormat = f }
	p.ClearMarks = p.handleClearMarks
	return p
}

// SetScope mirrors app.ScopeChangedMsg for tests, which is its only
// caller: production rescopes through the message so Update runs and
// the visual-range anchor check goes with it.
func (p *Page) SetScope(s string) {
	p.Scope = s
	p.recompute()
}

func (p *Page) Init() tea.Cmd { return p.Spinner.Tick }

// In-flight requests finish: CreateSilence is non-idempotent so
// cancelling mid-flight risks a half-created silence.
func (p *Page) Close() tea.Cmd {
	if p.cancelBulk != nil {
		p.cancelBulk()
		p.cancelBulk = nil
	}
	return nil
}

func (*Page) Crumb() string { return resourceAlerts }

func (p *Page) Title() string {
	if p.SpinnerActive(p.ScopeIncludes) {
		return p.LoadingTitle(resourceAlerts, p.styles.Header.Accent)
	}
	scope := p.Scope
	if scope == "" {
		scope = listpage.ScopeAll
	}
	if p.FilterBuffer() != "" || p.stateFilter != "" {
		return fmt.Sprintf("alerts(%s)[%d/%d]", scope, len(p.groups), p.totalGroups())
	}
	return fmt.Sprintf("alerts(%s)[%d]", scope, len(p.groups))
}

func (p *Page) HeaderContent() string {
	var parts []string
	if p.FilterBuffer() != "" {
		parts = append(parts, "filter:"+p.FilterBuffer())
	}
	if p.stateFilter != "" {
		parts = append(parts, "state:"+p.stateFilter)
	}
	if n := len(p.marks); n > 0 {
		parts = append(parts, fmt.Sprintf("marked:%d", n))
	}
	if p.Visual.On() {
		parts = append(parts, listpage.ChipVisual)
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

// PollResources implements app.PollAwarePage.
func (*Page) PollResources() []string { return []string{resourceAlerts} }

// When read-only, Dangerous entries ('s') are stripped before returning.
func (p *Page) Bindings() []action.Action {
	guarded := p.guarded()
	sortBindings := p.sorter.Bindings(resourceAlerts)
	out := make([]action.Action, 0, 8+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "detail", View: resourceAlerts},
		action.Action{Key: "Space", Description: "mark", View: resourceAlerts, Shared: true},
		action.Action{Key: "Shift+V", Description: "mark range", View: resourceAlerts, Shared: true},
		action.Action{Key: "s", Description: "silence", View: resourceAlerts, Dangerous: true, Guarded: guarded},
		action.Action{Key: "/", Description: "filter", View: resourceAlerts},
		action.Action{Key: "Shift+F", Description: "state filter", View: resourceAlerts},
	)
	if table.HasWide(p.labelCols) {
		out = append(out, action.Action{Key: "Shift+W", Description: "wide", View: resourceAlerts})
	}
	out = append(out, sortBindings...)
	// 'r' is global; surface it here for discoverability.
	out = append(out,
		action.Action{Key: "Shift+T", Description: "state format", View: resourceAlerts},
		action.Action{Key: "r", Description: "refresh", View: resourceAlerts},
		action.Action{Key: "w", Description: "toggle watch", View: resourceAlerts},
		// Last on purpose: the keys do nothing on a terminal wide
		// enough for every column, and the fixed-size hint strip drops
		// the tail first.
		action.Action{Key: "Right", DisplayKey: "←/→", Description: "scroll columns", View: resourceAlerts},
	)
	if p.session.ReadOnly() {
		return action.FilterDangerous(out)
	}
	return out
}

// SPDX-License-Identifier: Apache-2.0

// Package groupdetail renders the L2 group-detail page — the live
// instance list reached by Enter on a multi-instance alert at L1.
//
// It is an ordinary list page (cursor, marks, sort, filter, polling),
// not a detail/scroll surface: rows are the individual
// backend.Alert instances sharing one (tenant, alertname). The page
// mirrors the alerts list's structure verbatim — cursor and marks
// keyed by fingerprint, severity/labels/state/age columns, bulk
// silence-one fanout — scoped to a single tenant and alertname.
//
// Columns are SEVERITY <distinguishing-labels> STATE AGE: no TENANT
// column (one tenant), no COUNT column (the title carries the
// instance tally). The flex column renders each instance's
// distinguishing labels (those outside the group's common set), with
// the `instance` label pinned first. A one-line common-labels strip
// renders above the table by default and collapses with Shift+C.
//
// Polling lives in the wiring layer: a poll loop emits
// DataMsg{Resource: []backend.Alert} for the page's tenant; the page
// keeps only the instances whose alertname matches and recomputes.
// See CONTEXT.md "Alert drill-down" and ADR 0038.
package groupdetail

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

// Sort column keys — stable identifiers passed to the tablesort
// helper, used as both the canonical Key (ArrowFor / IsActive
// lookups) and the lower-cased help-overlay description text. Order
// matches the h/l walk cycle.
const (
	sortKeySeverity = "severity"
	sortKeyInstance = "instance"
	sortKeyAge      = "age"
)

// viewName is the page's stable view identifier — the Crumb, the
// action-registry View tag, and the loading-affordance fallback noun
// all read from it so they never drift apart.
const viewName = "instances"

// instanceSortColumns returns the page's sortable column set.
// Severity defaults DESC (critical first); the rest read naturally
// ascending. Every comparator falls back to fingerprint ASC so the
// order is total and deterministic across re-sorts / poll ticks.
func instanceSortColumns() []tablesort.Column[instanceEntry] {
	return []tablesort.Column[instanceEntry]{
		{
			// Severity is the default column, so it needs no direct
			// hotkey — reachable via h/l. Crucially, `Shift+S` and `S`
			// share one KeyPressMsg.String() ("S") in bubbletea v2, and
			// ADR 0038 pins `S` to "open silences" as the verb that
			// stays consistent across L2/L3. A severity Hotkey of 'S'
			// would shadow that verb, so the column carries no hotkey
			// and DefaultAsc stays true: SeverityRank already ranks
			// critical highest, so an ascending sort over the negated
			// rank puts critical first without the DESC arg-flip that
			// would otherwise reverse the fingerprint tie-break.
			Key: sortKeySeverity, Title: "SEVERITY", Hotkey: 0, DefaultAsc: true,
			Description: "sort by severity",
			Less: tieBreakFingerprint(func(a, b *instanceEntry) bool {
				return backend.SeverityRank(a.a.Labels) > backend.SeverityRank(b.a.Labels)
			}),
		},
		{
			Key: sortKeyInstance, Title: "INSTANCE", Hotkey: 'N', DefaultAsc: true,
			Description: "sort by instance label",
			Less: tieBreakFingerprint(func(a, b *instanceEntry) bool {
				ai, bi := a.a.Labels["instance"], b.a.Labels["instance"]
				if ai != bi {
					return ai < bi
				}
				return a.distinguishSummary < b.distinguishSummary
			}),
		},
		{
			Key: sortKeyAge, Title: "AGE", Hotkey: 'A', DefaultAsc: true,
			Description: "sort by age",
			Less: tieBreakFingerprint(func(a, b *instanceEntry) bool {
				return a.a.StartsAt.Before(b.a.StartsAt)
			}),
		},
	}
}

// tieBreakFingerprint wraps a comparator so equal-by-primary entries
// fall back to fingerprint ASC. Without this, sort.SliceStable would
// preserve input order on ties — fine for cursor stickiness, but not
// a total order, so identical inputs in a different ingest order
// would render differently. Fingerprint is unique per instance, so
// the fallback yields one canonical layout.
func tieBreakFingerprint(primary func(a, b *instanceEntry) bool) func(a, b *instanceEntry) bool {
	return func(a, b *instanceEntry) bool {
		if primary(a, b) {
			return true
		}
		if primary(b, a) {
			return false
		}
		return a.a.Fingerprint < b.a.Fingerprint
	}
}

// Options bundles the per-page constructor inputs. Mirrors
// alert.Options / alerts.Options field-for-field where they overlap;
// Tenant / AlertName / Instances carry the L1 group seed.
type Options struct {
	Styles *theme.Styles
	// Now injects the wall clock for the age column. nil falls back
	// to time.Now.
	Now func() time.Time
	// Clients is the per-tenant write surface handed to the silence
	// form on `s`. Empty / missing tenant flashes a hint.
	Clients map[string]silenceform.Client
	// Creator seeds the silence form's CreatedBy field; empty falls
	// back to "a10r" in the form factory.
	Creator string
	// TimeFormat seeds the page's time-format mode at push so the
	// instance list opens in the same mode the parent showed.
	TimeFormat timerender.Format
	// StateFormat seeds the STATE column density at push so the page
	// opens in the same full/compact mode the L1 list showed.
	StateFormat stateformat.Format
	// ReadOnly hides the page's Dangerous bindings (`s`) and turns
	// the keystroke into a flash hint.
	ReadOnly bool
	// EditorResolver handles the Ctrl+E round-trip on the restricted
	// silences page pushed by `S`.
	EditorResolver edit.Resolver
	// EditorCtx is the parent ctx the editor subprocess inherits on
	// the pushed silences page. nil falls back to context.Background().
	EditorCtx context.Context //nolint:containedctx // editor subprocess ctx, plumbed once at construction.
	// BulkCtx parents the bulk silence-one fanout. nil falls back to
	// context.Background().
	BulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.
	// SubmitCtx parents the silence form's submit ctx. nil falls back
	// to context.Background() inside the form.
	SubmitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
	// BulkConcurrency caps the per-tenant worker pool for the bulk
	// silence-one fanout. Zero resolves to config.DefaultBulkConcurrency.
	BulkConcurrency int
	// Logger receives per-failure detail from the bulk fanout. Nil
	// suppresses logging.
	Logger *slog.Logger
	// Tenant is the single backend this page is scoped to.
	Tenant string
	// AlertName is the alertname every instance on this page shares.
	AlertName string
	// Instances seeds the view at push time so the page renders
	// immediately, before the first poll tick. Replaced wholesale by
	// each in-tenant DataMsg's alertname-filtered subset.
	Instances []backend.Alert
}

// instanceEntry wraps one alert instance with the precomputed
// strings the filter and sort hot-paths read, so neither walks the
// label map on every keystroke / comparison.
type instanceEntry struct {
	a backend.Alert
	// lowerComposite is the lower-cased label+annotation blob the
	// substring filter matches against. Built once at recompute.
	lowerComposite string
	// distinguishSummary is the rendered `k=v · k=v` distinguishing-
	// label string used for both the row cell and the instance-sort
	// tie-break. Built once at recompute against the stable common set.
	distinguishSummary string
}

// Page is the L2 group-detail instance list. Implements app.Page.
type Page struct {
	listpage.Base
	listpage.PollingUI

	styles *theme.Styles
	now    func() time.Time

	clients map[string]silenceform.Client
	creator string

	tenant    string
	alertName string

	// instances is the current full instance set (the alertname-
	// matched subset of the last in-tenant DataMsg, seeded from
	// Options at construction). common and the view derive from it.
	instances []backend.Alert
	// common is the label intersection across ALL instances, stable
	// regardless of the active filter. Recomputed on every data
	// change so the strip and the distinguishing-label projection
	// agree.
	common map[string]string
	view   []instanceEntry

	// focusFingerprint anchors the cursor across recomputes. Empty
	// when nothing is focused.
	focusFingerprint string
	// marks is the set of Space-toggled fingerprints for the bulk
	// silence-one fanout.
	marks map[string]struct{}

	pendingBulkSilence pendingBulkSilence
	bulkConcurrency    int
	logger             *slog.Logger
	cancelBulk         context.CancelFunc

	sorter      *tablesort.Sorter[instanceEntry]
	stateFilter string
	timeFormat  timerender.Format
	stateFormat stateformat.Format
	// commonCollapsed hides the common-labels strip when true. Shown
	// by default; toggled page-locally by Shift+C (no app broadcast).
	commonCollapsed bool

	readOnly bool

	bulkCtx   context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.
	submitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.

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
			Scope:         opts.Tenant,
			BackendHealth: map[string]listpage.BackendHealth{},
			Tenants:       []string{opts.Tenant},
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
		tenant:          opts.Tenant,
		alertName:       opts.AlertName,
		instances:       append([]backend.Alert(nil), opts.Instances...),
		common:          map[string]string{},
		sorter:          tablesort.New(instanceSortColumns(), sortKeySeverity),
		marks:           map[string]struct{}{},
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
		readOnly:        opts.ReadOnly,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
		editorResolver:  opts.EditorResolver,
		editorCtx:       opts.EditorCtx,
	}
	p.Recompute = p.recompute
	p.RowCount = func() int { return len(p.view) }
	p.SnapshotFocus = p.snapshotFocus
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	p.SetStateFormat = func(f stateformat.Format) { p.stateFormat = f }
	p.ClearMarks = p.handleClearMarks
	p.recompute()
	return p
}

// Init kicks the spinner so the cold-start loading affordance
// animates until the first DataMsg lands. The seed instances render
// immediately regardless.
func (p *Page) Init() tea.Cmd { return p.Spinner.Tick }

// Close cancels any in-flight bulk silence-one fanout so a page pop
// mid-air aborts not-yet-started work. In-flight HTTP requests finish
// — CreateSilence is non-idempotent. Mirrors the alerts page.
func (p *Page) Close() tea.Cmd {
	if p.cancelBulk != nil {
		p.cancelBulk()
		p.cancelBulk = nil
	}
	return nil
}

func (*Page) Crumb() string { return viewName }

// Title is "<AlertName>(<tenant>)[N]" with N the instance count;
// "[N/M]" when a filter is active. During a loading window the
// title flips to the loading affordance.
func (p *Page) Title() string {
	if p.SpinnerActive(p.ScopeIncludes) {
		return p.LoadingTitle(p.titleNoun())
	}
	total := len(p.instances)
	if p.Filter != "" || p.stateFilter != "" {
		return fmt.Sprintf("%s(%s)[%d/%d]", p.alertName, p.tenant, len(p.view), total)
	}
	return fmt.Sprintf("%s(%s)[%d]", p.alertName, p.tenant, total)
}

// titleNoun is the loading-affordance noun — the alertname when
// known so the loading border still identifies the alert.
func (p *Page) titleNoun() string {
	if p.alertName != "" {
		return p.alertName
	}
	return viewName
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

// Footer is the refresh countdown surface.
func (p *Page) Footer() string {
	return listpage.RefreshCountdown(
		p.Paused, p.Refreshing,
		p.PolledInScope(p.ScopeIncludes),
		p.SoonestNextRefresh(p.ScopeIncludes),
		p.now(),
	)
}

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache replays "alerts" payloads into this page on push.
func (*Page) PollResources() []string { return []string{"alerts"} }

// Bindings returns the per-view hint-strip / help-overlay actions.
// Sort shortcuts come from the tablesort helper; h/l column walk
// lives on every table view via TableMotions and isn't repeated.
// Dangerous entries (`s`) are stripped in read-only mode.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings(viewName)
	out := make([]action.Action, 0, 8+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "detail", View: viewName},
		action.Action{Key: "Space", Description: "mark", View: viewName, Shared: true},
		action.Action{Key: "s", Description: "silence", View: viewName, Dangerous: true},
		action.Action{Key: "S", Description: "open silences", View: viewName},
		action.Action{Key: "/", Description: "filter", View: viewName},
		action.Action{Key: "Shift+F", Description: "state filter", View: viewName},
		action.Action{Key: "Shift+C", Description: "common labels", View: viewName},
	)
	out = append(out, sortBindings...)
	out = append(out,
		action.Action{Key: "Shift+T", Description: "state format", View: viewName},
		action.Action{Key: "r", Description: "refresh", View: viewName},
		action.Action{Key: "w", Description: "toggle watch", View: viewName},
	)
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

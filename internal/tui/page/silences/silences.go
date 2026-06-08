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
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

const (
	sortKeyEndsAt    = "ends"
	sortKeyStartsAt  = "starts"
	sortKeyCreatedBy = "by"
	sortKeyState     = "state"
)

const (
	colHeaderStarts = "STARTS"
	colHeaderEnds   = "ENDS"
	colHeaderState  = "STATE"
)

const resourceSilences = "silences"

const editorExtensionYAML = "yaml"

func silenceSortColumns() []tablesort.Column[silenceEntry] {
	return []tablesort.Column[silenceEntry]{
		{
			Key: sortKeyCreatedBy, Title: "BY", Hotkey: 'C', DefaultAsc: true,
			Description: "sort by creator",
			Less:        func(a, b *silenceEntry) bool { return a.s.CreatedBy < b.s.CreatedBy },
		},
		{
			Key: sortKeyStartsAt, Title: colHeaderStarts, Hotkey: 'S', DefaultAsc: true,
			Description: "sort by startsAt",
			Less:        func(a, b *silenceEntry) bool { return a.s.StartsAt.Before(b.s.StartsAt) },
		},
		{
			Key: sortKeyEndsAt, Title: colHeaderEnds, Hotkey: 'E', DefaultAsc: true,
			Description: "sort by endsAt",
			Less:        func(a, b *silenceEntry) bool { return a.s.EndsAt.Before(b.s.EndsAt) },
		},
		{
			Key: sortKeyState, Title: colHeaderState, Hotkey: 'T', DefaultAsc: true,
			Less: func(a, b *silenceEntry) bool { return a.s.State < b.s.State },
		},
	}
}

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

type Page struct {
	listpage.Base
	listpage.PollingUI

	styles *theme.Styles
	now    func() time.Time

	// byTenant holds the most recent snapshot for each backend
	// keyed by the poll.DataMsg.Tenant tag. Pages built in single-
	// backend setups end up with one entry; multi-backend ones
	// accumulate every snapshot they've received.
	byTenant map[string][]backend.Silence
	view     []silenceEntry

	// sorter: comparators from silenceSortColumns.
	sorter  *tablesort.Sorter[silenceEntry]
	focusID string

	// clients are the per-tenant write surfaces the page hands to
	// the silence form when the user presses `n` and calls
	// ExpireSilence on directly when the user presses `x` / `Ctrl+X`.
	// Empty in tests or read-only runs — write actions flash a
	// hint instead.
	clients map[string]silenceform.Client
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

	// timeFormat is flipped by app.TimeFormatChangedMsg so all list pages agree.
	timeFormat timerender.Format

	// pendingEdit captures which silence the user is editing in
	// $EDITOR so the FinishedMsg handler can call UpdateSilence
	// against the right backend. Empty between rounds.
	pendingEdit pendingEdit

	// bulkConcurrency: tenants always parallel; this limits the inner pool per tenant.
	bulkConcurrency int
	// logger is the structured logger used for per-failure detail
	// in the bulk fanout. Nil suppresses logging — the page never
	// crashes on a missing logger.
	logger *slog.Logger
	// cancelBulk: Close() calls it so a page pop short-circuits not-yet-started workers.
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
	// until app shutdown. Mirrors the per-write cancel pattern used
	// by the silence form and tenantconfig.
	cancelEditorUpdate context.CancelFunc

	// restrictIDs is the frozen set of silence IDs the page is
	// allowed to surface. nil when no restriction is active (see
	// Options.RestrictIDs and ADR 0035).
	restrictIDs map[string]struct{}
	alertName   string
	alertLabels map[string]string

	// readOnly: Bindings() filters Dangerous; handleAction flashes a hint.
	readOnly bool

	// editorCtx is the parent context the editor subprocess
	// inherits when the user presses Ctrl+E. Wired to the
	// program's RunE ctx so a parent shutdown aborts a hung
	// editor session. nil falls back to context.Background()
	// inside edit.Edit.
	editorCtx context.Context //nolint:containedctx // editor subprocess ctx, not session state.
	// bulkCtx: wired to RunE ctx so a quit cancels in-flight workers. nil falls back to context.Background().
	bulkCtx context.Context //nolint:containedctx // bulk fanout ctx, plumbed once at construction.

	// submitCtx parents the silence form's submit ctx. See
	// Options.SubmitCtx for the rationale.
	submitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
}

type Options struct {
	Styles  *theme.Styles
	Now     func() time.Time
	Clients map[string]silenceform.Client
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
	TimeFormat timerender.Format
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
	// inherits. Cancelling kills the editor so a parent shutdown
	// can abort a hung session. nil falls back to
	// context.Background() inside edit.Edit.
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
	// RestrictIDs, when non-empty, hides every row whose Silence.ID
	// is not in the set. Frozen at construction time. Used by the
	// alert-detail S binding to mirror the alert's silenced-by list
	// — see CONTEXT.md "Restricted silences view" and ADR 0035.
	RestrictIDs []string
	// AlertName, when non-empty, substitutes for the scope label in
	// Title() so the page reads "silences(<alertName>)" instead of
	// "silences(<scope>)". Only meaningful alongside RestrictIDs.
	AlertName string
	// AlertLabels, when non-empty, seeds the silence form's matcher
	// list on `n` via matcher.FromLabels — same prefill as
	// alert-detail `s`. Only meaningful alongside RestrictIDs.
	AlertLabels map[string]string
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
			Scope:         listpage.ScopeAll,
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
		editor:          opts.EditorResolver,
		timeFormat:      opts.TimeFormat,
		byTenant:        map[string][]backend.Silence{},
		marks:           map[string]struct{}{},
		sorter:          tablesort.New(silenceSortColumns(), sortKeyEndsAt),
		bulkConcurrency: concurrency,
		logger:          opts.Logger,
		readOnly:        opts.ReadOnly,
		editorCtx:       opts.EditorCtx,
		bulkCtx:         opts.BulkCtx,
		submitCtx:       opts.SubmitCtx,
		alertName:       opts.AlertName,
		alertLabels:     opts.AlertLabels,
	}
	if len(opts.RestrictIDs) > 0 {
		p.restrictIDs = make(map[string]struct{}, len(opts.RestrictIDs))
		for _, id := range opts.RestrictIDs {
			p.restrictIDs[id] = struct{}{}
		}
	}
	p.Recompute = p.recompute
	p.RowCount = func() int { return len(p.view) }
	p.SnapshotFocus = p.snapshotFocus
	p.SetTimeFormat = func(f timerender.Format) { p.timeFormat = f }
	p.ClearMarks = p.handleClearMarks
	return p
}

func (p *Page) Init() tea.Cmd { return p.Spinner.Tick }

// Close implements app.Page. Cancels any in-flight bulk-expire
// fanout so a page-pop while workers are mid-air aborts not-yet-
// started work via the worker channel select. In-flight HTTP
// requests are allowed to finish; expire is idempotent on the AM
// side so completing them is safe. Also cancels any in-flight
// editor-driven UpdateSilence so a page-pop mid-write aborts the
// request instead of letting the goroutine outlive the page —
// same per-write cancel contract as the silence form and
// tenantconfig.
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

func (*Page) Crumb() string { return resourceSilences }

func (p *Page) Title() string {
	if p.SpinnerActive(p.ScopeIncludes) {
		return p.LoadingTitle(resourceSilences)
	}
	scope := p.alertName
	if scope == "" {
		scope = p.Scope
		if scope == "" {
			scope = listpage.ScopeAll
		}
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
func (*Page) PollResources() []string { return []string{resourceSilences} }

// When read-only, Dangerous entries are stripped before returning.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings(resourceSilences)
	out := make([]action.Action, 0, 8+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "detail", View: resourceSilences},
		action.Action{Key: "n", Description: "new", View: resourceSilences, Dangerous: true},
		action.Action{Key: "e", Description: "edit", View: resourceSilences, Dangerous: true},
		action.Action{Key: "x", Description: "expire (cursor / marks)", View: resourceSilences, Dangerous: true},
		action.Action{Key: "Space", Description: "mark", View: resourceSilences, Shared: true},
		action.Action{Key: "Ctrl+E", Description: "editor", View: resourceSilences, Dangerous: true},
		action.Action{Key: "Ctrl+N", Description: "recreate (expired)", View: resourceSilences, Dangerous: true},
	)
	out = append(out, sortBindings...)
	out = append(out,
		action.Action{Key: "r", Description: "refresh", View: resourceSilences},
		action.Action{Key: "w", Description: "toggle watch", View: resourceSilences},
	)
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

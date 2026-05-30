// SPDX-License-Identifier: Apache-2.0

// Package groups renders the alert-groups view: a two-level tree
// where each group label-set expands to its member alerts. Enter
// on a group toggles expand/collapse; Enter on a leaf drills to
// the alert-detail page (DrillAlertMsg). `s` pushes the silence
// form prefilled with the group's common-labels intersection.
package groups

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/tablesort"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// DrillAlertMsg is emitted on Enter against a leaf row. The wiring
// layer pushes the alert detail page.
type DrillAlertMsg struct {
	Alert backend.Alert
}

// Sort column keys. Stable identifiers passed to the tablesort
// helper — the title slug ("name", "count", "severity") matches
// what the help overlay renders ("sort by <title>").
const (
	sortKeyName     = "name"
	sortKeyCount    = "count"
	sortKeySeverity = "severity"
)

// groupSortColumns returns the page's sortable axes. Count and
// severity default DESC (noisiest / worst groups land first —
// triage priority); name defaults ASC (alphabetical reading
// order). Ties on count / severity fall back to name-asc so the
// ordering stays deterministic across refreshes.
func groupSortColumns() []tablesort.Column[groupEntry] {
	nameLess := func(a, b *groupEntry) bool {
		return labelSummary(a.g.Labels) < labelSummary(b.g.Labels)
	}
	return []tablesort.Column[groupEntry]{
		{
			Key: sortKeyName, Title: "NAME", Hotkey: 'N', DefaultAsc: true,
			Less: nameLess,
		},
		{
			Key: sortKeyCount, Title: "COUNT", Hotkey: 'C', DefaultAsc: false,
			Description: "sort by alert count",
			Less: func(a, b *groupEntry) bool {
				ai, bi := len(a.g.Alerts), len(b.g.Alerts)
				if ai != bi {
					return ai < bi
				}
				return nameLess(a, b)
			},
		},
		{
			// `s` is the silence verb on this page; severity uses
			// Shift+V (mnemonic for "severity") to dodge the
			// collision.
			Key: sortKeySeverity, Title: "SEVERITY", Hotkey: 'V', DefaultAsc: false,
			Less: func(a, b *groupEntry) bool {
				if a.severityRank != b.severityRank {
					return a.severityRank < b.severityRank
				}
				return nameLess(a, b)
			},
		},
	}
}

// groupEntry pairs an alert group with the tenant tag it was
// polled under so the renderer can show which backend each group
// belongs to when the active scope spans more than one tenant.
//
// severityRank caches the worst-severity rank across the group's
// alerts so the severity comparator doesn't walk the alert slice
// on every comparison; recompute populates it once per poll. Zero
// is the natural rest value (no severity → unknown).
//
// common caches the labels shared by every alert in the group so
// leaf rendering can compute distinguishing labels in O(alert) per
// frame rather than O(alert × group) by re-deriving the
// intersection on every render.
type groupEntry struct {
	g            backend.AlertGroup
	tenant       string
	severityRank int
	common       map[string]string
	// lowerSummary caches the lower-cased labelSummary used by the
	// substring filter. Computed once at recompute so rows() and
	// visibleGroups() don't re-run strings.ToLower(labelSummary())
	// on every frame.
	lowerSummary string
}

// Options bundles the page's constructor inputs. Clients is the
// per-tenant write surface the page hands to the silence form on
// `s`; empty in tests / read-only runs flashes a hint instead of
// pushing a broken form. Same shape the alerts / silences pages
// consume.
type Options struct {
	Styles *theme.Styles
	// Now injects the form's clock. nil falls back to time.Now in
	// the silenceform constructor.
	Now func() time.Time
	// Clients is the per-tenant write surface the page hands to
	// the silence form. Picked up by the cursor row's tenant
	// (groupEntry.tenant), set when the poller emits DataMsg.
	Clients map[string]silenceform.Client
	// Creator seeds the form's CreatedBy field; usually $USER.
	Creator string
	// ReadOnly hides the page's Dangerous bindings (`s`) from the
	// hint strip / help overlay and turns the keystroke into a
	// flash hint. Wired from defaults.read_only / --read-only.
	ReadOnly bool
	// Tenants is the canonical list of configured backend names.
	// Drives the TENANT-column visibility decision so a tenant
	// that never replies (cold-start connection refused) still
	// counts toward "is this a multi-tenant fleet?". Empty falls
	// back to inferring the count from observed DataMsgs.
	Tenants []string
	// SubmitCtx is the parent ctx the silence form's submit ctx
	// derives from. Plumbed through to silenceform.Options.SubmitCtx
	// so an app-level shutdown propagates through the form's
	// in-flight Create/UpdateSilence write — not only through the
	// page-pop / Close cascade. nil falls back to
	// context.Background() inside the form.
	SubmitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
}

// Page is the groups view.
type Page struct {
	listpage.Base
	listpage.PollingUI

	styles *theme.Styles
	now    func() time.Time

	// clients is the per-tenant write surface for `s`; see Options.
	clients map[string]silenceform.Client
	// creator seeds the silence form's CreatedBy field.
	creator string

	// byTenant holds the most recent snapshot per backend.
	byTenant map[string][]backend.AlertGroup
	// flattened cache of in-scope groups, rebuilt on every
	// recompute. Indices into this slice are what `expanded` and
	// `cursor` reference.
	flat     []groupEntry
	expanded []bool // per-group flag, indexed against p.flat

	// cachedRows is the materialised rows() result. Invalidated
	// (set to nil) by every state change that would alter the
	// rendered row list: recompute, filter change, expand toggle.
	// rows() rebuilds and re-caches when it sees a nil cache.
	cachedRows []row

	// sorter owns the active sort axis and direction. Default:
	// name ASC — alphabetical label-set, matching the historical
	// implicit ordering. Mirrors the alerts / silences page state
	// shape so handleSort can be the same shape across the three
	// list pages.
	sorter *tablesort.Sorter[groupEntry]

	// focusKey is the identity of the row the cursor was on before
	// the most recent recompute — used to restore the cursor onto
	// the same logical row after a re-sort, scope change, or poll
	// refresh. Without it, pressing Shift+C would jump to whatever
	// group lands at the cursor's numeric index. Mirrors
	// focusFingerprint / focusID on alerts / silences.
	focusKey string

	// readOnly mirrors Options.ReadOnly. Bindings() filters
	// Dangerous entries when set; handleAction flashes a hint
	// instead of pushing the silence form.
	readOnly bool

	// submitCtx parents the silence form's submit ctx. See
	// Options.SubmitCtx for the rationale.
	submitCtx context.Context //nolint:containedctx // silence-form submit ctx, plumbed once at construction.
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
		styles:    opts.Styles,
		now:       now,
		clients:   opts.Clients,
		creator:   opts.Creator,
		byTenant:  map[string][]backend.AlertGroup{},
		sorter:    tablesort.New(groupSortColumns(), sortKeyName),
		readOnly:  opts.ReadOnly,
		submitCtx: opts.SubmitCtx,
	}
	p.Recompute = p.recompute
	p.RowCount = func() int { return len(p.rows()) }
	p.SnapshotFocus = p.snapshotFocus
	return p
}

// Init kicks the spinner so the cold-start "loading" affordance
// animates until the first DataMsg lands.
func (p *Page) Init() tea.Cmd { return p.Spinner.Tick }

func (*Page) Close() tea.Cmd { return nil }

func (*Page) Crumb() string { return "groups" }

// Title implements app.Page. Mirrors the alerts shape:
// `groups(<scope>)[<count>]` or `groups(<scope>)[F/T]` while a
// filter is active. During a loading window the title flips to the
// loading affordance so the border itself reads as the loading
// state, k9s-style.
func (p *Page) Title() string {
	if p.SpinnerActive(p.ScopeIncludes) {
		return p.LoadingTitle("groups")
	}
	scope := p.Scope
	if scope == "" {
		scope = listpage.ScopeAll
	}
	total := p.totalGroups()
	visible := len(p.visibleGroups())
	if p.Filter != "" {
		return fmt.Sprintf("groups(%s)[%d/%d]", scope, visible, total)
	}
	return fmt.Sprintf("groups(%s)[%d]", scope, visible)
}

func (p *Page) HeaderContent() string {
	if p.Filter != "" {
		return "filter:" + p.Filter
	}
	return ""
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
// snapshot cache only replays "groups" payloads into this page
// on push.
func (*Page) PollResources() []string { return []string{"groups"} }

// Bindings implements app.Page. Sort shortcuts come from the
// tablesort helper so all list pages emit them identically; the
// page contributes the page-specific verbs (Enter / s / Tab / r)
// around them.
//
// When the page is in read-only mode the Dangerous entries are
// stripped before the slice is returned so the hint strip and
// help overlay both render the read-only verb set.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("groups")
	out := make([]action.Action, 0, 4+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "expand / drill", View: "groups"},
		action.Action{Key: "s", Description: "silence group", View: "groups", Dangerous: true},
		action.Action{Key: "Tab", Description: "expand all", View: "groups"},
	)
	out = append(out, sortBindings...)
	out = append(out,
		action.Action{Key: "r", Description: "refresh", View: "groups"},
		action.Action{Key: "w", Description: "toggle watch", View: "groups"},
	)
	if p.readOnly {
		return action.FilterDangerous(out)
	}
	return out
}

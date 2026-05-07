// SPDX-License-Identifier: Apache-2.0

// Package groups renders the alert-groups view: a two-level tree
// where each group label-set expands to its member alerts. Enter
// on a group toggles expand/collapse; Enter on a leaf drills to
// the alert-detail page (DrillAlertMsg). `s` pushes the silence
// form prefilled with the group's common-labels intersection.
package groups

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
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
	nameLess := func(a, b groupEntry) bool {
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
			Less: func(a, b groupEntry) bool {
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
			Less: func(a, b groupEntry) bool {
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
}

// Options bundles the page's constructor inputs. Clients is the
// per-tenant write surface the page hands to the silence form on
// `s`; empty in tests / read-only runs flashes a hint instead of
// pushing a broken form. Same shape the alerts / silences pages
// consume.
type Options struct {
	Styles theme.Styles
	// Now injects the form's clock. nil falls back to time.Now in
	// the silenceform constructor.
	Now func() time.Time
	// Clients is the per-tenant write surface the page hands to
	// the silence form. Picked up by the cursor row's tenant
	// (groupEntry.tenant), set when the poller emits DataMsg.
	Clients map[string]silenceform.Client
	// Creator seeds the form's CreatedBy field; usually $USER.
	Creator string
}

// Page is the groups view.
type Page struct {
	styles theme.Styles
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
	cursor   int    // index into the visible row list
	topRow   int    // first visible row; reconciled in renderRows

	// bodyHeight is the row capacity snapshotted on the most recent
	// View. Ctrl+D / Ctrl+U step half this; Ctrl+F / Ctrl+B step
	// body-2 (vim's CTRL-F two-line overlap convention). Zero before
	// the first render — handlers fall back to 10 / 20 so a keystroke
	// that beats the initial WindowSizeMsg still moves a sane distance.
	bodyHeight int

	// filter is the active substring filter applied to a group's
	// label-set (k=v pairs joined). preFilter is the snapshot the
	// page restores on PromptCancelledMsg per the shared
	// `/`-prompt contract (see alerts page for the lifecycle doc).
	// Filtering operates at group granularity: a group either is
	// or isn't in the rendered list; expanding a matched group
	// shows every alert it carries, unfiltered.
	filter    string
	preFilter *string

	// scope mirrors the active tenant scope.
	scope string

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

	// polledTenants / nextRefresh / refreshing / spinner mirror the
	// alerts and silences pages' polling UX so the three list pages
	// frame identically. See alerts.Page for the design notes.
	polledTenants map[string]struct{}
	nextRefresh   map[string]time.Time
	refreshing    bool
	spinner       spinner.Model
}

// scopeAll is the canonical "every configured tenant" label.
const scopeAll = "all"

// New constructs an empty groups page.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	sp := spinner.New(
		spinner.WithSpinner(spinner.Points),
		spinner.WithStyle(opts.Styles.Header.Accent),
	)
	return &Page{
		styles:        opts.Styles,
		now:           now,
		clients:       opts.Clients,
		creator:       opts.Creator,
		byTenant:      map[string][]backend.AlertGroup{},
		scope:         scopeAll,
		polledTenants: map[string]struct{}{},
		nextRefresh:   map[string]time.Time{},
		spinner:       sp,
		sorter:        tablesort.New(groupSortColumns(), sortKeyName),
	}
}

// Init implements app.Page. Kicks the spinner so the cold-start
// "loading" affordance animates while the first poll tick lands.
func (p *Page) Init() tea.Cmd { return p.spinner.Tick }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "groups" }

// Title implements app.Page. Mirrors the alerts shape:
// `groups(<scope>)[<count>]` or `groups(<scope>)[F/T]` while a
// filter is active. While the page is in a loading window —
// cold start (no DataMsg yet) or a manual `r` refresh in flight
// — the title flips to the spinner-led "loading groups…" so the
// border itself reads as the loading affordance, k9s-style.
func (p *Page) Title() string {
	if !p.polled() || p.refreshing {
		return p.spinner.View() + " loading groups…"
	}
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	total := p.totalGroups()
	visible := len(p.visibleGroups())
	if p.filter != "" {
		return fmt.Sprintf("groups(%s)[%d/%d]", scope, visible, total)
	}
	return fmt.Sprintf("groups(%s)[%d]", scope, visible)
}

// totalGroups is the in-scope count regardless of filter.
func (p *Page) HeaderContent() string {
	if p.filter != "" {
		return "filter:" + p.filter
	}
	return ""
}

// Footer implements app.Page. Renders the next-refresh deadline
// — or "refreshing…" while a manual `r` is in flight — into the
// bordered body's bottom edge. Same shape as alerts / silences.
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

// soonestNextRefresh returns the earliest in-scope DataMsg.NextAt.
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
// renders as "due" so a slow tick reads honestly.
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

// polled reports whether at least one in-scope tenant has produced
// a DataMsg yet. Scope-aware to avoid flickering out of loading
// state on a multi-backend setup with a narrowed scope.
func (p *Page) polled() bool {
	for tenant := range p.polledTenants {
		if p.scopeIncludes(tenant) {
			return true
		}
	}
	return false
}

// spinnerActive reports whether the spinner should keep ticking.
func (p *Page) spinnerActive() bool { return !p.polled() || p.refreshing }

// PollResources implements app.PollAwarePage so the App-level
// snapshot cache only replays "groups" payloads into this page
// on push.
func (*Page) PollResources() []string { return []string{"groups"} }

// Bindings implements app.Page. Sort shortcuts come from the
// tablesort helper so all list pages emit them identically; the
// page contributes the page-specific verbs (Enter / s / Tab / r)
// around them.
func (p *Page) Bindings() []action.Action {
	sortBindings := p.sorter.Bindings("groups")
	out := make([]action.Action, 0, 4+len(sortBindings))
	out = append(out,
		action.Action{Key: "Enter", Description: "expand / drill", View: "groups"},
		action.Action{Key: "s", Description: "silence group", View: "groups", Dangerous: true},
		action.Action{Key: "Tab", Description: "expand all", View: "groups"},
	)
	out = append(out, sortBindings...)
	out = append(out, action.Action{Key: "r", Description: "refresh", View: "groups"})
	return out
}

// SPDX-License-Identifier: Apache-2.0

// Package silences renders the silences list page. The page
// surfaces the Silence write actions (new, edit, expire, editor)
// behind Dangerous bindings so read-only mode hides them all.
//
// Single-row writes (`n`, `e`, `x`) operate on the cursor row;
// `Ctrl+X` bulk-expires every Space-marked row. Destructive verbs
// (`x` / `Ctrl+X`) round-trip through a confirm modal with the
// default-No safe choice so a stray Enter never destroys data.
// `Ctrl+E` opens the silence in $EDITOR (or $A10R_EDITOR) — that
// path lands in a follow-up commit.
package silences

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// SortKey enumerates the sortable columns for the silences table.
type SortKey int

const (
	// SortByEndsAt is the default — silences expiring soonest at
	// the top (E2).
	SortByEndsAt SortKey = iota
	// SortByStartsAt sorts by start time.
	SortByStartsAt
	// SortByCreatedBy sorts alphabetically by creator.
	SortByCreatedBy
	// SortByState sorts by silence state (active, pending, expired).
	SortByState
)

// String returns the column-header label.
func (s SortKey) String() string {
	switch s {
	case SortByEndsAt:
		return "ends"
	case SortByStartsAt:
		return "starts"
	case SortByCreatedBy:
		return "by"
	case SortByState:
		return "state"
	}
	return "?"
}

// Client is the write surface the silences page needs: it pushes
// the silence form (silenceform.Client) on `n` / `e` and calls
// ExpireSilence on `x` / `Ctrl+X`. Bundled at the page level
// rather than added to silenceform.Client because expire isn't
// part of the form's contract — the form never expires anything.
// backend.Client satisfies this interface for free; tests inject
// a small fake.
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

	sort    SortKey
	sortAsc bool
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

	// pendingEdit captures which silence the user is editing in
	// $EDITOR so the FinishedMsg handler can call UpdateSilence
	// against the right backend. Empty between rounds.
	pendingEdit pendingEdit
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
}

// New constructs an empty silences page.
func New(opts Options) *Page {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Page{
		styles:   opts.Styles,
		now:      now,
		clients:  opts.Clients,
		creator:  opts.Creator,
		editor:   opts.EditorResolver,
		byTenant: map[string][]backend.Silence{},
		marks:    map[string]struct{}{},
		sort:     SortByEndsAt,
		sortAsc:  true, // soonest-expiring first
		scope:    scopeAll,
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

// Init implements app.Page.
func (*Page) Init() tea.Cmd { return nil }

// Close implements app.Page.
func (*Page) Close() tea.Cmd { return nil }

// Crumb implements app.Page.
func (*Page) Crumb() string { return "silences" }

// Title implements app.Page. Mirrors the alerts page's shape:
// `silences(<scope>)[<count>]` or `silences(<scope>)[F/T]` while
// a filter is active.
func (p *Page) Title() string {
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
// been applied or queued without re-opening the prompt.
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

// Bindings implements app.Page. Every write action carries
// Dangerous so read-only mode (C4) hides them via the action
// registry.
func (*Page) Bindings() []action.Action {
	return []action.Action{
		{Key: "n", Description: "new", View: "silences", Dangerous: true},
		{Key: "e", Description: "edit", View: "silences", Dangerous: true},
		{Key: "x", Description: "expire", View: "silences", Dangerous: true},
		{Key: "Space", Description: "mark", View: "silences"},
		{Key: "Ctrl+E", Description: "editor", View: "silences", Dangerous: true},
		{Key: "Ctrl+X", Description: "bulk expire", View: "silences", Dangerous: true, Bulk: true},
	}
}

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.DataMsg:
		s, ok := m.Resource.([]backend.Silence)
		if !ok {
			return p, nil
		}
		p.byTenant[m.Tenant] = s
		p.recompute()
		return p, nil
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return p, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		p.snapshotFocus()
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash so the user has visual
		// confirmation. The next poll tick surfaces the change in
		// the list. Updated picks "updated" vs. "created" so an
		// edit doesn't read like a duplicate creation.
		verb := "created"
		if m.Updated {
			verb = "updated"
		}
		return p, flashFn(footer.FlashSuccess, "silence "+verb+": "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. No flash — form Esc is a
		// non-event from the user's perspective.
		return p, nil
	case modal.ConfirmResultMsg:
		cmd := p.handleExpireConfirm(m)
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
		p.cursor = min(p.cursor+10, max(len(p.view)-1, 0))
		p.snapshotFocus()
	case "ctrl+u":
		p.cursor = max(p.cursor-10, 0)
		p.snapshotFocus()
	default:
		return false
	}
	return true
}

// handleSort processes sort-column shortcuts. Same column twice
// flips ASC↔DESC; switching to a new column resets direction to
// that column's default — matching the spreadsheet-style UX.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	switch m.String() {
	case "shift+e", "E":
		p.applySort(SortByEndsAt)
	case "shift+s", "S":
		p.applySort(SortByStartsAt)
	case "shift+c", "C":
		p.applySort(SortByCreatedBy)
	case "shift+t", "T":
		p.applySort(SortByState)
	default:
		return false
	}
	return true
}

// applySort updates sort key and direction. Same key twice flips
// ASC↔DESC; new key resets to that column's default direction.
func (p *Page) applySort(k SortKey) {
	if p.sort == k {
		p.sortAsc = !p.sortAsc
	} else {
		p.sort = k
		p.sortAsc = defaultAsc(k)
	}
	p.recompute()
}

// defaultAsc returns the direction the column reads naturally
// when first activated. EndsAt is ASC so soonest-expiring shows
// first (the operator-priority "what's about to come back");
// everything else is also ASC for consistency.
func defaultAsc(_ SortKey) bool { return true }

func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "n":
		cmd := p.openNewSilenceForm()
		return p, cmd
	case "e":
		cmd := p.openEditSilenceForm()
		return p, cmd
	case "x":
		cmd := p.openExpireConfirm()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "ctrl+e":
		cmd := p.openEditorForCursor()
		return p, cmd
	case "ctrl+x":
		cmd := p.openBulkExpireConfirm()
		return p, cmd
	}
	return p, nil
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
	question := fmt.Sprintf("expire %d marked silences?", len(ids))
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultNo)
	})
}

// handleExpireConfirm consumes a ConfirmResultMsg arriving after
// an expire confirm modal. Yes drives ExpireSilence on every
// pending {id, tenant} captured at modal-open time. No /
// Cancelled clears the pending state silently. Tenants are read
// directly from the captured pair — no live-view lookup — so a
// poll tick or filter change between Open and Yes never reroutes
// the expire to the wrong backend or drops it as "unknown".
func (p *Page) handleExpireConfirm(m modal.ConfirmResultMsg) tea.Cmd {
	pending := p.pendingExpire
	p.pendingExpire = pendingExpire{}
	if m.Cancelled || !m.Yes || len(pending.ids) == 0 {
		return nil
	}
	success := 0
	failed := 0
	for _, target := range pending.ids {
		client, ok := p.clients[target.tenant]
		if !ok {
			failed++
			continue
		}
		if err := client.ExpireSilence(context.Background(), target.id); err != nil {
			failed++
			continue
		}
		success++
	}
	// Clear marks for every queued ID — a failed expire isn't a
	// signal to keep the silence marked for retry; the user can
	// re-mark deliberately.
	for _, target := range pending.ids {
		delete(p.marks, target.id)
	}
	return p.flashExpireResult(pending.bulk, success, failed)
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
// flash text for a completed expire round. Called from
// handleExpireConfirm so single-row x and bulk Ctrl+X share one
// wording table.
func (p *Page) flashExpireResult(bulk bool, success, failed int) tea.Cmd {
	if !bulk {
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
	return flashFn(footer.FlashWarn, fmt.Sprintf("expired %d of %d — %d failed", success, success+failed, failed))
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
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
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
	if len(p.view) == 0 {
		return p.styles.Body.Default.Width(width).Height(height).Render(p.emptyState())
	}
	headerLine := p.renderHeader(width)
	rows := p.renderRows(width, height-1)
	body := headerLine + "\n" + rows
	return lipgloss.NewStyle().Width(width).Render(body)
}

func (p *Page) emptyState() string {
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
// When marks are present, a leading "  " (two cols) reserves
// space so the data columns stay aligned with the row mark
// glyph below.
func (p *Page) renderHeader(width int) string {
	titles := []SortKey{SortByEndsAt, SortByStartsAt, SortByCreatedBy, SortByState}
	parts := make([]string, 0, len(titles)+1)
	if p.showTenantColumn() {
		parts = append(parts, "TENANT")
	}
	for _, k := range titles {
		label := strings.ToUpper(k.String())
		if k == p.sort {
			arrow := "↓"
			if p.sortAsc {
				arrow = "↑"
			}
			label = label + " " + arrow
		}
		parts = append(parts, label)
	}
	leading := ""
	if p.hasMarks() {
		// Match the per-row mark width ("✓ " / "  ") so columns
		// stay aligned. Two cols, foreground-only render so the
		// strip blends with the body background.
		leading = "  "
	}
	// Foreground-only render so the header row keeps the body
	// background — flush with the data rows underneath rather
	// than a coloured stripe.
	return lipgloss.NewStyle().
		Foreground(p.styles.Table.Header.GetForeground()).
		Render(leading + p.padColumns(parts, width))
}

// hasMarks reports whether any silence ID is currently marked.
// Inlined-style helper so the renderer can branch without
// poking at p.marks length in two places.
func (p *Page) hasMarks() bool { return len(p.marks) > 0 }

func (p *Page) renderRows(width, maxRows int) string {
	if maxRows <= 0 || len(p.view) == 0 {
		return ""
	}
	p.reconcileScroll(maxRows)
	end := min(p.topRow+maxRows, len(p.view))
	showMark := p.hasMarks()
	var b strings.Builder
	for i := p.topRow; i < end; i++ {
		e := p.view[i]
		row := make([]string, 0, 5)
		if p.showTenantColumn() {
			row = append(row, e.tenant)
		}
		row = append(row,
			header.FormatAge(p.now(), e.s.EndsAt),
			header.FormatAge(p.now(), e.s.StartsAt),
			e.s.CreatedBy,
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
		// Precedence: cursor wins over marked (the user's "you are
		// here" signal beats the queued-for-bulk signal); marked
		// rows that aren't under the cursor get a fg-only tint via
		// styles.Table.Marked so they stand out without competing
		// with the cursor highlight.
		line := padRight(prefix+mark+p.padColumns(row, width), width)
		switch {
		case i == p.cursor:
			line = p.styles.Table.Cursor.Render(line)
		case marked:
			line = lipgloss.NewStyle().
				Foreground(p.styles.Table.Marked.GetForeground()).
				Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// reconcileScroll keeps p.cursor inside [topRow, topRow+maxRows).
// Same shape as the alerts page; replicated rather than shared so
// each page stays self-contained until a tablekit emerges.
func (p *Page) reconcileScroll(maxRows int) {
	if p.cursor < p.topRow {
		p.topRow = p.cursor
	}
	if p.cursor >= p.topRow+maxRows {
		p.topRow = p.cursor - maxRows + 1
	}
	maxTop := max(len(p.view)-maxRows, 0)
	if p.topRow > maxTop {
		p.topRow = maxTop
	}
	if p.topRow < 0 {
		p.topRow = 0
	}
}

// padColumns lays out a row across fixed-width columns. The
// optional leading TENANT column shrinks the flex CreatedBy
// column so the totals still fit the available width.
func (p *Page) padColumns(parts []string, width int) string {
	const (
		tenantW = 16
		endsW   = 14
		startsW = 14
		stateW  = 12
		minBy   = 10
	)
	used := endsW + startsW + stateW + 2
	cols := make([]int, 0, 5)
	if p.showTenantColumn() {
		cols = append(cols, tenantW)
		used += tenantW
	}
	flex := max(width-used, minBy)
	cols = append(cols, endsW, startsW, flex, stateW)
	var b strings.Builder
	for i, v := range parts {
		if i >= len(cols) {
			break
		}
		b.WriteString(padRight(v, cols[i]))
	}
	return b.String()
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
	sortSilences(p.view, p.sort, p.sortAsc)
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
// caller must already be lowercased.
func silenceMatches(s backend.Silence, q string) bool {
	if strings.Contains(strings.ToLower(s.CreatedBy), q) ||
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

func sortSilences(out []silenceEntry, key SortKey, asc bool) {
	less := lessFor(key)
	sort.SliceStable(out, func(i, j int) bool {
		if asc {
			return less(out[i].s, out[j].s)
		}
		return less(out[j].s, out[i].s)
	})
}

func lessFor(key SortKey) func(a, b backend.Silence) bool {
	switch key {
	case SortByStartsAt:
		return func(a, b backend.Silence) bool { return a.StartsAt.Before(b.StartsAt) }
	case SortByCreatedBy:
		return func(a, b backend.Silence) bool { return a.CreatedBy < b.CreatedBy }
	case SortByState:
		return func(a, b backend.Silence) bool { return a.State < b.State }
	default: // SortByEndsAt
		return func(a, b backend.Silence) bool { return a.EndsAt.Before(b.EndsAt) }
	}
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

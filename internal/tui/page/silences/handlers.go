// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	silencepage "github.com/wilfriedroset/a10r/internal/tui/page/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"

	"charm.land/bubbles/v2/spinner"
)

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.handleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		p.HandleBackendStatusMsg(m)
		return p, nil
	case poll.DataMsg:
		s, ok := m.Resource.([]backend.Silence)
		if !ok {
			return p, nil
		}
		// Same tenant-validation guard as BackendStatusMsg above.
		if !p.KnownTenant(m.Tenant) {
			return p, nil
		}
		// Watch-mode: paused pages drop the snapshot so the table
		// does not move under the cursor mid-read. A pending
		// pausedRefresh from a manual `r` press lets a single tick
		// through and clears itself, so the operator can pull
		// fresh data on demand without leaving paused state.
		if p.Paused && !p.PausedRefresh {
			return p, nil
		}
		p.PausedRefresh = false
		p.byTenant[m.Tenant] = s
		// Capture the poll's NextAt so the bottom-border Footer can
		// render "next refresh 25s" without a parallel ticker.
		// Zero-valued (legacy / test) DataMsgs leave the per-tenant
		// entry intact rather than clobbering it with a zero —
		// keeps the footer stable when a unit test fakes only the
		// resource payload.
		if !m.NextAt.IsZero() {
			p.NextRefresh[m.Tenant] = m.NextAt
		}
		p.PolledTenants[m.Tenant] = struct{}{}
		// Only clear refreshing once an in-scope tenant has
		// answered — an out-of-scope DataMsg arriving during a
		// manual `r` window would otherwise drop the spinner
		// before the user has actually seen fresh data for the
		// scope they're looking at.
		if p.ScopeIncludes(m.Tenant) {
			p.Refreshing = false
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
		p.Spinner, cmd = p.Spinner.Update(m)
		return p, cmd
	case silenceform.SubmittedMsg, silenceform.CancelledMsg, modal.ConfirmResultMsg, bulkExpireDoneMsg:
		_ = m // multi-type case: handleWriteResult consumes msg via its own type switch
		cmd := p.handleWriteResult(msg)
		return p, cmd
	case edit.FinishedMsg:
		cmd := p.handleEditorFinished(m)
		return p, cmd
	case editorUpdateResultMsg:
		cmd := p.handleEditorUpdateResult(m)
		return p, cmd
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.HandleFilterPrompt(m)
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
		auditSilenceWrite(verb, m.ID, "form")
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
		p.HandleScopeChangedMsg(m)
		return true, nil
	case app.TimeFormatChangedMsg:
		p.timeFormat = m.Format
		return true, nil
	case app.GoToFirstRowMsg:
		p.SetIndex(0, len(p.view))
		p.snapshotFocus()
		return true, nil
	case app.ClearMarksMsg:
		return true, p.handleClearMarks()
	}
	return false, nil
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
	changed, handled := p.MoveCursor(m.String(), len(p.view))
	if !handled {
		return false
	}
	if changed {
		p.snapshotFocus()
	}
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
	return cursor.HandleSort(
		m.String(),
		p.sorter,
		func() { p.focusID = "" },
		p.recompute,
	)
}

func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "enter":
		cmd := p.drillToDetail()
		return p, cmd
	case "n":
		cmd := p.runWriteAction(p.openNewSilenceForm)
		return p, cmd
	case "e":
		cmd := p.runWriteAction(p.openEditSilenceForm)
		return p, cmd
	case "x":
		cmd := p.runWriteAction(p.openExpireConfirmUnified)
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "ctrl+e":
		cmd := p.runWriteAction(p.openEditorForCursor)
		return p, cmd
	case "ctrl+n":
		cmd := p.runWriteAction(p.openRecreateSilenceForm)
		return p, cmd
	case "r":
		cmd := p.requestRefresh()
		return p, cmd
	case "w":
		p.toggleWatch()
		return p, nil
	}
	return p, nil
}

// toggleWatch flips paused state. When un-pausing, also clear any
// pending pausedRefresh — the next DataMsg should be treated
// normally because the page is no longer paused. When pausing
// without an in-flight `r` press, leave pausedRefresh false so
// the next ordinary DataMsg is silently dropped. Mirrors the
// alerts page's helper.
func (p *Page) toggleWatch() {
	p.Paused = !p.Paused
	if !p.Paused {
		p.PausedRefresh = false
	}
}

// runWriteAction is the read-only gate applied to every Dangerous
// keypress on the page. When the page is read-only it short-circuits
// with a single Warn flash; otherwise it dispatches the wrapped
// handler. Centralised here so the read-only contract has one
// touch-point and a stray new write verb cannot bypass it.
func (p *Page) runWriteAction(action func() tea.Cmd) tea.Cmd {
	if p.readOnly {
		return flashFn(footer.FlashWarn, hintReadOnly)
	}
	return action()
}

// hintReadOnly is the flash text emitted when a write keystroke
// fires in read-only mode. Singular noun keeps it short enough to
// fit the footer flash slot without wrapping on a 80-col terminal.
const hintReadOnly = "read-only mode — silences cannot be modified"

// drillToDetail returns a Cmd that pushes the silence-detail page
// for the row under the cursor. Empty view falls through to a soft
// Info flash so the user sees a reason for the no-op.
func (p *Page) drillToDetail() tea.Cmd {
	if p.Index() >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	entry := p.view[p.Index()]
	styles := p.styles
	return app.PushPage(func() app.Page {
		return silencepage.New(silencepage.Options{
			Silence: entry.s,
			Tenant:  entry.tenant,
			Styles:  styles,
		})
	})
}

// requestRefresh emits a RefreshRequestedMsg so the wiring layer
// pokes the silences pollers, flips the page into refreshing
// state, and (re)kicks the spinner Tick chain. Mirror of the
// alerts page's helper.
//
// When paused, sets pausedRefresh so the next incoming DataMsg
// is honoured exactly once — the operator pulled it deliberately
// and expects to see fresh data even though watch mode is off.
func (p *Page) requestRefresh() tea.Cmd {
	p.Refreshing = true
	if p.Paused {
		p.PausedRefresh = true
	}
	scope := p.Scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "silences", Scope: scope}
	}
	return tea.Batch(emit, p.Spinner.Tick)
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
	if p.Index() >= len(p.view) {
		return
	}
	id := p.view[p.Index()].s.ID
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
// row to edit if no row is focused. Per ADR-0011 the form's
// Tenant row renders read-only in edit mode (the AM v2 API does
// not move silences between tenants), so the whole p.clients map
// is handed through unchanged — the form's tenantDisabled logic
// freezes the selection on entry.tenant.
func (p *Page) openEditSilenceForm() tea.Cmd {
	if p.Index() >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	entry := p.view[p.Index()]
	if _, ok := p.clients[entry.tenant]; !ok {
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
	clients := silenceformClients(p.clients)
	tenant := entry.tenant
	s := entry.s
	submitCtx := p.submitCtx
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:   clients,
			Tenant:    tenant,
			Styles:    styles,
			Now:       now,
			Creator:   creator,
			Matchers:  s.Matchers,
			Comment:   s.Comment,
			EndsAt:    s.EndsAt,
			EditID:    s.ID,
			SubmitCtx: submitCtx,
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
	if p.Index() >= len(p.view) {
		return silenceform.Options{}, flashFn(footer.FlashInfo, "no silence under the cursor"), false
	}
	if len(p.clients) == 0 {
		return silenceform.Options{}, flashFn(footer.FlashWarn, hintNoWriteableBackend), false
	}
	entry := p.view[p.Index()]
	if entry.s.State != backend.SilenceStateExpired {
		return silenceform.Options{}, flashFn(footer.FlashInfo,
			"only expired silences can be recreated — use `e` to edit a live silence"), false
	}
	if _, ok := p.clients[entry.tenant]; !ok {
		return silenceform.Options{}, flashFn(footer.FlashWarn, hintNoWriteableBackend), false
	}
	return silenceform.Options{
		Clients:   silenceformClients(p.clients),
		Tenant:    entry.tenant,
		Styles:    p.styles,
		Now:       p.now,
		Creator:   p.defaultCreator(),
		Matchers:  entry.s.Matchers,
		Comment:   entry.s.Comment,
		BlankEnds: true,
		FocusEnds: true,
		SubmitCtx: p.submitCtx,
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

// pendingEdit is the in-flight state between an opened editor
// session and its FinishedMsg. id is the silence ID; tenant is
// the backend the silence belongs to (cached at open time so a
// poll-tick reordering between open and save still routes the
// update correctly).
type pendingEdit struct {
	id     string
	tenant string
}

// editorUpdateResultMsg carries the outcome of the asynchronous
// editor-driven UpdateSilence call. Dispatched from the tea.Cmd that
// runs the backend write off the bubbletea Update goroutine so a slow
// 5xx doesn't freeze the TUI. The handler in Update() turns this
// into the flash/audit/reopen branching the previous synchronous
// path did inline. content + pending are carried through so the
// error branch can reopen the editor with the user's typed YAML.
type editorUpdateResultMsg struct {
	id      string
	content string
	pending pendingEdit
	err     error
}

// openEditorForCursor hands the cursor silence to the user's
// $EDITOR via the page's edit.Resolver. Captures the silence
// (id, tenant, snapshot) into p.pendingEdit so the FinishedMsg
// handler can route UpdateSilence at the right backend after the
// editor returns.
func (p *Page) openEditorForCursor() tea.Cmd {
	if p.Index() >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.editor.EditorEnv) == 0 && p.editor.DefaultEditor == "" {
		return flashFn(footer.FlashWarn, "editor handoff requires $EDITOR or $A10R_EDITOR")
	}
	entry := p.view[p.Index()]
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
		Ctx:        p.editorCtx,
	})
}

// handleEditorFinished consumes a FinishedMsg arriving after an
// $EDITOR session. Branches:
//   - Err set: flash and clear pending state. The silence stays
//     unchanged.
//   - Empty / unchanged content: silent no-op (the user :q'd
//     without writing).
//   - YAML id != pending.id (audit F8): refuse the update, flash
//     an error, and reopen the editor with the user's buffer
//     preserved so they can fix the typo without losing their work.
//     pendingEdit is intentionally left in place so the second
//     attempt updates the same silence.
//   - Otherwise: parse YAML, call UpdateSilence on the pending
//     tenant's client, flash success / error.
func (p *Page) handleEditorFinished(m edit.FinishedMsg) tea.Cmd {
	if m.Err != nil {
		p.pendingEdit = pendingEdit{}
		return flashFn(footer.FlashError, "editor: "+m.Err.Error())
	}
	if strings.TrimSpace(m.Content) == "" {
		p.pendingEdit = pendingEdit{}
		return nil
	}
	pending := p.pendingEdit
	id, spec, err := silenceFromYAML([]byte(m.Content))
	if err != nil {
		p.pendingEdit = pendingEdit{}
		return flashFn(footer.FlashError, "yaml: "+err.Error())
	}
	if pending.id != "" && id != "" && id != pending.id {
		// Keep pendingEdit so the reopened editor session updates
		// the same silence the original `Ctrl+E` targeted. Pre-fill
		// the editor with the user's just-edited content rather
		// than the original snapshot — losing their work to a typo
		// would be hostile UX.
		flash := flashFn(footer.FlashError,
			"silence id mismatch — expected "+pending.id+", got "+id+"; reopening editor")
		reopen := p.editor.Edit(edit.Request{
			ResourceID: pending.id,
			Initial:    m.Content,
			Extension:  "yaml",
			Ctx:        p.editorCtx,
		})
		return tea.Batch(flash, reopen)
	}
	// pendingEdit is intentionally NOT cleared yet: if UpdateSilence
	// (or the no-writeable-backend guard below) fails, we reopen the
	// editor with the user's typed content preserved, mirroring the
	// id-mismatch path above. Losing the user's edits to a transient
	// 5xx is the user-pain that motivates this branch.
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
		// No retry path the user can drive from here: the tenant
		// vanished between open and save. Clear pendingEdit and
		// flash. Content is sacrificed, but the user can re-open
		// the silence after fixing config.
		p.pendingEdit = pendingEdit{}
		return flashFn(footer.FlashError, "no writeable backend for silence "+id)
	}
	// Dispatch the write asynchronously so a slow backend doesn't
	// freeze the bubbletea Update loop. The result lands as an
	// editorUpdateResultMsg that handleEditorUpdateResult turns
	// into the appropriate flash + audit (success) or flash + reopen
	// (failure, content preserved via msg.content).
	//
	// Wire a cancellable ctx (mu-guarded cancel stored on the page)
	// so Close() aborts the in-flight UpdateSilence instead of
	// letting the goroutine outlive the page. The parent is the
	// editorCtx when set so an app-level shutdown still propagates;
	// context.Background() otherwise. Mirrors the silence-form
	// (7b8aa88) / tenantconfig (adca17d) cancel-on-Close contract.
	parent := p.editorCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.mu.Lock()
	if p.cancelEditorUpdate != nil {
		// A previous editor write was somehow still in flight;
		// cancel it so we don't have two writes racing.
		p.cancelEditorUpdate()
	}
	p.cancelEditorUpdate = cancel
	p.mu.Unlock()
	clearCancel := func() {
		p.mu.Lock()
		p.cancelEditorUpdate = nil
		p.mu.Unlock()
		cancel()
	}
	content := m.Content
	return func() tea.Msg {
		defer clearCancel()
		err := client.UpdateSilence(ctx, id, spec)
		return editorUpdateResultMsg{
			id:      id,
			content: content,
			pending: pending,
			err:     err,
		}
	}
}

// handleEditorUpdateResult resolves the async UpdateSilence
// outcome. On success, clears pendingEdit and audits + flashes;
// on failure, keeps pendingEdit and reopens the editor with the
// user's typed YAML preserved (mirrors the id-mismatch retry
// pattern). See editorUpdateResultMsg for why this is split out.
func (p *Page) handleEditorUpdateResult(m editorUpdateResultMsg) tea.Cmd {
	if m.err != nil {
		flash := flashFn(footer.FlashError, "update: "+m.err.Error())
		reopen := p.editor.Edit(edit.Request{
			ResourceID: m.pending.id,
			Initial:    m.content,
			Extension:  "yaml",
			Ctx:        p.editorCtx,
		})
		return tea.Batch(flash, reopen)
	}
	p.pendingEdit = pendingEdit{}
	auditSilenceWrite("updated", m.id, "editor")
	return flashFn(footer.FlashSuccess, "silence updated: "+m.id)
}

// openNewSilenceForm pushes an empty silence form targeting the
// best-fit backend. Initial selection rule per ADR-0011: the cursor
// row's tenant (when a row is focused), else the first in-scope
// tenant from p.clients in alphabetical order. The user can still
// change the target via Enter on the Tenant row inside the form.
// Empty p.clients (no backends configured, or read-only run)
// flashes a hint instead.
func (p *Page) openNewSilenceForm() tea.Cmd {
	tenant, _, ok := p.pickWriteTarget()
	if !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	creator := p.defaultCreator()
	now := p.now
	styles := p.styles
	clients := silenceformClients(p.clients)
	submitCtx := p.submitCtx
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:   clients,
			Tenant:    tenant,
			Styles:    styles,
			Now:       now,
			Creator:   creator,
			SubmitCtx: submitCtx,
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

// silenceformClients projects the page's Client map (which embeds
// silenceform.Client plus ExpireSilence) onto the narrower
// silenceform.Client map the form takes. Per ADR-0011 the form
// receives the full writeable set so the user can pick across
// tenants from inside the form without the caller pre-resolving.
// The projection is by-key, sharing the underlying value
// references — the map is short-lived and read-only on the form
// side, so no copying / locking concern.
func silenceformClients(in map[string]Client) map[string]silenceform.Client {
	out := make(map[string]silenceform.Client, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// pickWriteTarget returns the tenant + client to send a write to.
// Cursor row's tenant wins when a row is focused; otherwise falls
// back to the first in-scope tenant (alphabetical for stability).
// Returns (_, _, false) when nothing usable is configured.
func (p *Page) pickWriteTarget() (string, Client, bool) {
	if len(p.clients) == 0 {
		return "", nil, false
	}
	if p.Index() < len(p.view) {
		t := p.view[p.Index()].tenant
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
		if p.ScopeIncludes(t) {
			return t, p.clients[t], true
		}
	}
	return "", nil, false
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

// auditSilenceWrite emits the structured "silence write succeeded"
// log line surfaced on every successful silence mutation so an
// operator can reconstruct the day's activity from the --log file.
// Closes the F4 attack-chain tail flagged in the re-audit's G1
// (logger plumbing was wired but no success-path entry existed).
//
// Routed through slog.Default() — runTUI calls slog.SetDefault on
// the program's logger before any page is constructed, so every
// page sees the same sink without each having to thread a pointer.
//
// op is the verb ("created", "updated", "expired"); surface
// records the screen/handler that drove it ("form", "editor",
// "bulk-expire") so a future correlation against the user's input
// stays unambiguous. The wire-level key is "surface" (not "source")
// because slog reserves "source" for the SourceKey caller-info
// attribute, and sloglint forbids re-using the name.
func auditSilenceWrite(op, id, surface string) {
	slog.Default().Info("silence write succeeded",
		slog.String("op", op),
		slog.String("id", id),
		slog.String("surface", surface))
}

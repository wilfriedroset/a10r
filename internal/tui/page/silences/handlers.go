// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	silencepage "github.com/wilfriedroset/a10r/internal/tui/page/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"

	"charm.land/bubbles/v2/spinner"
)

// Update wraps the message switch so every path that rebuilds the
// view is followed by the anchor check — a filter change, a poll
// refresh or a scope switch can drop the row an open range hangs
// off, and the preview must not outlive it.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	next, cmd := p.handleMsg(msg)
	return next, tea.Batch(cmd, listpage.CancelVisualOnLostAnchor(&p.Base, p.view, markKey))
}

func (p *Page) handleMsg(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		p.HandleBackendStatusMsg(m)
		return p, nil
	case poll.DataMsg:
		listpage.ApplyDataMsg(&p.Base, &p.PollingUI, m, func(tenant string, s []backend.Silence) {
			p.byTenant[tenant] = s
		})
		return p, nil
	case spinner.TickMsg:
		// Forward only while the spinner is meaningful. Outside the
		// cold-start / refresh-in-flight windows we drop the tick,
		// which breaks the spinner's self-perpetuating Tick chain
		// and stops the per-frame redraw cost.
		if !p.SpinnerActive(p.ScopeIncludes) {
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
// confirm modal result, bulk-expire fanout result).
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
		return footer.ShowFlash(footer.FlashSuccess, "silence "+verb+": "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. No flash — form Esc is a
		// non-event from the user's perspective.
		return nil
	case modal.ConfirmResultMsg:
		if cmd, mine := p.applyEditConfirm(m); mine {
			return cmd
		}
		return p.handleExpireConfirm(m)
	case bulkExpireDoneMsg:
		return p.handleBulkExpireDone(m)
	}
	return nil
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
		cmd := p.runWriteAction(guardrail.ActionSilenceCreate, p.openNewSilenceForm)
		return p, cmd
	case "e":
		cmd := p.runWriteAction(guardrail.ActionSilenceUpdate, p.openEditSilenceForm)
		return p, cmd
	case "x", "delete":
		cmd := p.runWriteAction(guardrail.ActionSilenceExpire, p.openExpireConfirmUnified)
		return p, cmd
	case "space":
		listpage.MarkOrCommit(&p.Base, p.view, p.marks, markKey)
	case "V":
		listpage.StartOrCommitVisual(&p.Base, p.view, p.marks, markKey)
	case "ctrl+e":
		cmd := p.runWriteAction(guardrail.ActionSilenceUpdate, p.openEditorForCursor)
		return p, cmd
	case "ctrl+n":
		cmd := p.runWriteAction(guardrail.ActionSilenceRecreate, p.openRecreateSilenceForm)
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

func (p *Page) toggleWatch() { listpage.ToggleWatch(&p.Base, &p.PollingUI) }

// runWriteAction is the read-only and write-policy gate applied to
// every Dangerous keypress on the page. Read-only is checked first and
// always wins, so a rule is never quoted on a backend that cannot be
// written to at all (guardrails spec item 11). Centralised here so both
// contracts have one touch-point and a stray new write verb cannot
// bypass them. name is a guardrail.ActionSilence* constant, never a
// hand-written string: a name no rule can match would fail open.
func (p *Page) runWriteAction(name string, action func() tea.Cmd) tea.Cmd {
	if p.session.ReadOnly() {
		return footer.ShowFlash(footer.FlashWarn, hintReadOnly)
	}
	if name == guardrail.ActionSilenceExpire {
		// Expire is the one verb that fans out over marks, and an open
		// range is not marked until something commits it. The commit
		// runs here so the policy sees the rows the press would really
		// reach, and after the read-only gate so a read-only refusal
		// leaves the range alone. A guardrail refusal keeps the marks
		// on purpose, so the user can narrow them.
		listpage.CommitVisual(&p.Base, p.view, p.marks, markKey)
	}
	if d := p.session.Guardrails().Decide(p.writeRequest(name)); d.Refused() {
		return footer.ShowFlash(footer.FlashWarn, d.Flash())
	}
	return action()
}

// writeRequest asks about the run a press would really fire, so the
// duplicate tenants are the per-tenant count the cap compares against.
func (p *Page) writeRequest(name string) guardrail.Request {
	return p.request(name, p.markedTargets)
}

// guarded answers the [guarded] suffix. It counts each marked tenant
// once: a binding outlives any one run, so a cap the current marks
// happen to breach must not strike the key off the hint strip.
// Bindings() runs on the render path and again on every key press, so
// an unconfigured policy pays for no walk at all.
func (p *Page) guarded(name string) bool {
	rules := p.session.Guardrails()
	if len(rules) == 0 {
		return false
	}
	return rules.Decide(p.request(name, p.markedTenants)).Refused()
}

// request turns the press into its targets; marked resolves the bulk
// fan-out, the one case the callers count differently.
//
// Lead follows the press rather than the rule, so the sentence names
// the key the user pressed. A bulk expire is the only press this page
// has a name for; every other one reads back as the rule to edit.
func (p *Page) request(name string, marked func() []string) guardrail.Request {
	switch {
	case name == guardrail.ActionSilenceExpire && len(p.marks) > 0:
		return guardrail.Request{Action: name, Tenants: marked(), Lead: "bulk expire"}
	case name == guardrail.ActionSilenceCreate:
		if t, _, ok := p.pickWriteTarget(); ok {
			return guardrail.Request{Action: name, Tenants: []string{t}}
		}
	case p.Index() < len(p.view):
		return guardrail.Request{Action: name, Tenants: []string{p.view[p.Index()].tenant}}
	}
	return guardrail.Request{Action: name}
}

// markedTargets names the tenant of every marked silence, once per
// silence and in a stable order. It walks byTenant rather than the
// filtered view because openBulkExpireConfirm queues from byTenant
// too: a mark the active filter hides still reaches the write, so the
// policy has to count the same rows. A mark on a tenant with no
// writeable client is kept for the same reason — refusing a press that
// would have failed anyway costs nothing, and dropping it would let a
// capped tenant through whenever its client is missing.
func (p *Page) markedTargets() []string {
	var out []string
	for tenant, sils := range p.byTenant {
		for _, s := range sils {
			if _, marked := p.marks[s.ID]; marked {
				out = append(out, tenant)
			}
		}
	}
	slices.Sort(out)
	return out
}

// markedTenants serves the callers that ask per backend rather than
// per row.
func (p *Page) markedTenants() []string {
	var out []string
	for tenant, sils := range p.byTenant {
		for _, s := range sils {
			if _, marked := p.marks[s.ID]; marked {
				out = append(out, tenant)
				break
			}
		}
	}
	slices.Sort(out)
	return out
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
		return footer.ShowFlash(footer.FlashInfo, "no silence under the cursor")
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

func (p *Page) requestRefresh() tea.Cmd {
	return listpage.RequestRefresh(&p.Base, &p.PollingUI, resourceSilences)
}

// handleClearMarks drops every mark on the page in response to
// the global Ctrl+\ binding. Flashes "marks cleared" when the
// pre-clear count was non-zero so the user sees confirmation;
// silently no-ops otherwise.
func (p *Page) handleClearMarks() tea.Cmd {
	p.Visual.Cancel()
	if len(p.marks) == 0 {
		return nil
	}
	p.marks = map[string]struct{}{}
	return footer.ShowFlash(footer.FlashInfo, "marks cleared")
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
		return footer.ShowFlash(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.clients) == 0 {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	entry := p.view[p.Index()]
	if _, ok := p.clients[entry.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
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
	clients := p.clients
	tenant := entry.tenant
	s := entry.s
	submitCtx := p.submitCtx
	sess := p.session
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:    clients,
			Tenant:     tenant,
			Styles:     styles,
			Now:        now,
			Creator:    creator,
			Matchers:   s.Matchers,
			Comment:    s.Comment,
			EndsAt:     s.EndsAt,
			EditID:     s.ID,
			Guardrails: sess.Guardrails(),
			Action:     guardrail.ActionSilenceUpdate,
			SubmitCtx:  submitCtx,
		})
	})
}

// recreateFormOptions assembles the silenceform.Options for a
// recreate-expired flow against the cursor row. Returns
// (opts, nil, true) on a recreatable row; on any refusal, returns
// (zero, flash, false) so the caller dispatches the refusal flash.
// Creator is the current user — recreate is a new silence, not a
// re-authored copy of the source's CreatedBy.
func (p *Page) recreateFormOptions() (silenceform.Options, tea.Cmd, bool) {
	if p.Index() >= len(p.view) {
		return silenceform.Options{}, footer.ShowFlash(footer.FlashInfo, "no silence under the cursor"), false
	}
	if len(p.clients) == 0 {
		return silenceform.Options{}, footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend), false
	}
	entry := p.view[p.Index()]
	if entry.s.State != backend.SilenceStateExpired {
		return silenceform.Options{}, footer.ShowFlash(footer.FlashInfo,
			"only expired silences can be recreated — use `e` to edit a live silence"), false
	}
	if _, ok := p.clients[entry.tenant]; !ok {
		return silenceform.Options{}, footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend), false
	}
	return silenceform.Options{
		Clients:    p.clients,
		Tenant:     entry.tenant,
		Styles:     p.styles,
		Now:        p.now,
		Creator:    p.defaultCreator(),
		Matchers:   entry.s.Matchers,
		Comment:    entry.s.Comment,
		BlankEnds:  true,
		FocusEnds:  true,
		Guardrails: p.session.Guardrails(),
		Action:     guardrail.ActionSilenceRecreate,
		SubmitCtx:  p.submitCtx,
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
// update correctly). round tells two rounds on the same silence
// apart, so a write result only settles the round that sent it.
type pendingEdit struct {
	id     string
	tenant string
	round  uint64
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
		return footer.ShowFlash(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.editor.EditorEnv) == 0 && p.editor.DefaultEditor == "" {
		return footer.ShowFlash(footer.FlashWarn, "editor handoff requires $EDITOR or $A10R_EDITOR")
	}
	entry := p.view[p.Index()]
	if _, ok := p.clients[entry.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	if cmd, asked := p.confirmEdit(entry); asked {
		return cmd
	}
	return p.startEditor(entry)
}

// startEditor hands one captured row to the editor. It takes the row
// rather than reading the cursor, so a round resumed after a
// confirmation edits the silence the question named.
func (p *Page) startEditor(entry silenceEntry) tea.Cmd {
	body, err := silenceToYAML(entry.s)
	if err != nil {
		return footer.ShowFlash(footer.FlashError, "yaml encode: "+err.Error())
	}
	p.editRounds++
	p.pendingEdit = pendingEdit{id: entry.s.ID, tenant: entry.tenant, round: p.editRounds}
	return p.editor.Edit(edit.Request{
		ResourceID: entry.s.ID,
		Initial:    string(body),
		Extension:  editorExtensionYAML,
		Ctx:        p.editorCtx,
	})
}

// confirmEdit asks the confirmation a guardrail rule demands before
// the editor takes over the screen. Ctrl+E is the one silence.update
// route that never opens the form, so the prompt the form owns at
// submit has to live here as well, or the same verb would be asked on
// one route and waved through on the other. A deny is already refused
// upstream in runWriteAction.
func (p *Page) confirmEdit(entry silenceEntry) (tea.Cmd, bool) {
	d := p.session.Guardrails().Decide(guardrail.Request{
		Action:  guardrail.ActionSilenceUpdate,
		Tenants: []string{entry.tenant},
	})
	if d.Confirm == "" {
		return nil, false
	}
	captured := entry
	p.pendingEditConfirm = &captured
	return app.OpenModal(func() modal.Modal {
		return modal.NewGuardedConfirm("edit silence "+entry.s.ID+"?", modal.ConfirmDefaultNo, d.Typed)
	}), true
}

// applyEditConfirm claims a confirm result the editor prompt asked
// for. The expire flow reads the same message type, so the latch says
// which question the answer belongs to.
func (p *Page) applyEditConfirm(m modal.ConfirmResultMsg) (tea.Cmd, bool) {
	if p.pendingEditConfirm == nil {
		return nil, false
	}
	entry := *p.pendingEditConfirm
	p.pendingEditConfirm = nil
	if m.Cancelled || !m.Yes {
		return nil, true
	}
	return p.startEditor(entry), true
}

// handleEditorFinished consumes a FinishedMsg arriving after an
// $EDITOR session. Branches:
//   - Err set: flash and clear pending state. The silence stays
//     unchanged.
//   - Empty / unchanged content: silent no-op (the user :q'd
//     without writing).
//   - YAML id != pending.id (invariant: a typo'd id would mutate
//     the wrong silence): refuse the update, flash an error, and
//     reopen the editor with the user's buffer preserved so they
//     can fix the typo without losing their work. pendingEdit is
//     intentionally left in place so the second attempt updates
//     the same silence.
//   - Otherwise: parse YAML, call UpdateSilence on the pending
//     tenant's client, flash success / error.
func (p *Page) handleEditorFinished(m edit.FinishedMsg) tea.Cmd {
	if m.Err != nil {
		p.pendingEdit = pendingEdit{}
		return footer.ShowFlash(footer.FlashError, "editor: "+m.Err.Error())
	}
	if strings.TrimSpace(m.Content) == "" {
		p.pendingEdit = pendingEdit{}
		return nil
	}
	pending := p.pendingEdit
	id, spec, err := silenceFromYAML([]byte(m.Content))
	if err != nil {
		p.pendingEdit = pendingEdit{}
		return footer.ShowFlash(footer.FlashError, "yaml: "+err.Error())
	}
	if pending.id != "" && id != "" && id != pending.id {
		// Keep pendingEdit so the reopened editor session updates
		// the same silence the original `Ctrl+E` targeted. Pre-fill
		// the editor with the user's just-edited content rather
		// than the original snapshot — losing their work to a typo
		// would be hostile UX.
		flash := footer.ShowFlash(footer.FlashError,
			"silence id mismatch — expected "+pending.id+", got "+id+"; reopening editor")
		reopen := p.editor.Edit(edit.Request{
			ResourceID: pending.id,
			Initial:    m.Content,
			Extension:  editorExtensionYAML,
			Ctx:        p.editorCtx,
		})
		return tea.Batch(flash, reopen)
	}
	// pendingEdit is intentionally NOT cleared yet: if UpdateSilence
	// (or the no-writeable-backend guard below) fails, we reopen the
	// editor with the user's typed content preserved, mirroring the
	// id-mismatch path above. Losing the user's edits to a transient
	// 5xx is the user-pain that motivates this branch.
	if pending.tenant == "" {
		// Every round opens with its tenant, after the rule's
		// confirmation; writing a tenant picked now would skip it.
		p.pendingEdit = pendingEdit{}
		return footer.ShowFlash(footer.FlashError, "no editor round for silence "+id)
	}
	client, ok := p.clients[pending.tenant]
	if !ok {
		// No retry path the user can drive from here: the tenant
		// vanished between open and save. Clear pendingEdit and
		// flash. Content is sacrificed, but the user can re-open
		// the silence after fixing config.
		p.pendingEdit = pendingEdit{}
		return footer.ShowFlash(footer.FlashError, "no writeable backend for silence "+id)
	}
	return p.dispatchEditorUpdate(client, id, spec, pending, m.Content)
}

// dispatchEditorUpdate runs UpdateSilence asynchronously so a slow
// backend doesn't block Update. The mu-guarded cancel handle lets
// Close() abort the in-flight write; editorCtx propagates app-level
// shutdown when set. pending is threaded through so a failed write
// can reopen the editor with the user's content preserved.
func (p *Page) dispatchEditorUpdate(client silenceform.Client, id string, spec backend.SilenceSpec, pending pendingEdit, content string) tea.Cmd {
	parent := p.editorCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.mu.Lock()
	if p.cancelEditorUpdate != nil {
		// A second Ctrl+E saved while the earlier write was still in
		// flight; cancel it so we don't have two writes racing.
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
// outcome. On failure, the editor is reopened with the user's
// typed YAML preserved (same retry pattern as the id-mismatch path).
//
// A result can land after a second Ctrl+E opened a newer round,
// since nothing holds the key while a write is in flight. Such a
// result only reports: ending or reopening over the newer round
// would drop its tenant or bury its buffer.
func (p *Page) handleEditorUpdateResult(m editorUpdateResultMsg) tea.Cmd {
	if m.err != nil {
		flash := footer.ShowFlash(footer.FlashError, "update: "+m.err.Error())
		if m.pending != p.pendingEdit {
			return flash
		}
		reopen := p.editor.Edit(edit.Request{
			ResourceID: m.pending.id,
			Initial:    m.content,
			Extension:  editorExtensionYAML,
			Ctx:        p.editorCtx,
		})
		return tea.Batch(flash, reopen)
	}
	if m.pending == p.pendingEdit {
		p.pendingEdit = pendingEdit{}
	}
	auditSilenceWrite("updated", m.id, "editor")
	return footer.ShowFlash(footer.FlashSuccess, "silence updated: "+m.id)
}

// openNewSilenceForm pushes an empty silence form targeting the
// best-fit backend. Initial selection rule per ADR-0011: the cursor
// row's tenant (when a row is focused), else the first in-scope
// tenant from p.clients in alphabetical order. The user can still
// change the target via Enter on the Tenant row inside the form.
// Empty p.clients (no backends configured, or read-only run)
// flashes a hint instead. When alertLabels is non-empty (restricted
// silences view) the form's matchers are prefilled from the alert's
// labels — same prefill as alert-detail `s` (ADR 0035).
func (p *Page) openNewSilenceForm() tea.Cmd {
	tenant, _, ok := p.pickWriteTarget()
	if !ok {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	creator := p.defaultCreator()
	now := p.now
	styles := p.styles
	clients := p.clients
	submitCtx := p.submitCtx
	sess := p.session
	var matchers []backend.Matcher
	if len(p.alertLabels) > 0 {
		matchers = matcher.FromLabels(p.alertLabels)
	}
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:    clients,
			Tenant:     tenant,
			Styles:     styles,
			Now:        now,
			Creator:    creator,
			Matchers:   matchers,
			Guardrails: sess.Guardrails(),
			Action:     guardrail.ActionSilenceCreate,
			SubmitCtx:  submitCtx,
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

// pickWriteTarget returns the tenant + client to send a write to.
// Cursor row's tenant wins when a row is focused; otherwise falls
// back to the first in-scope tenant (alphabetical for stability).
// Returns (_, _, false) when nothing usable is configured.
func (p *Page) pickWriteTarget() (string, silenceform.Client, bool) {
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

// auditSilenceWrite emits the success-path audit record on every
// silence mutation so an operator can reconstruct the day's
// activity from the --log file (a tampered write would leave no
// trail otherwise). The wire key is "surface", not "source":
// sloglint forbids re-using slog's reserved SourceKey attribute.
func auditSilenceWrite(op, id, surface string) {
	slog.Default().Info("silence write succeeded",
		slog.String("op", op),
		slog.String("id", id),
		slog.String("surface", surface))
}

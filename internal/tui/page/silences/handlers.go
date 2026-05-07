// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"context"
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
	case poll.DataMsg:
		s, ok := m.Resource.([]backend.Silence)
		if !ok {
			return p, nil
		}
		p.byTenant[m.Tenant] = s
		// Capture the poll's NextAt so the bottom-border Footer can
		// render "next refresh 25s" without a parallel ticker.
		// Zero-valued (legacy / test) DataMsgs leave the per-tenant
		// entry intact rather than clobbering it with a zero —
		// keeps the footer stable when a unit test fakes only the
		// resource payload.
		if !m.NextAt.IsZero() {
			p.nextRefresh[m.Tenant] = m.NextAt
		}
		p.polledTenants[m.Tenant] = struct{}{}
		// Only clear refreshing once an in-scope tenant has
		// answered — an out-of-scope DataMsg arriving during a
		// manual `r` window would otherwise drop the spinner
		// before the user has actually seen fresh data for the
		// scope they're looking at.
		if p.scopeIncludes(m.Tenant) {
			p.refreshing = false
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
		p.spinner, cmd = p.spinner.Update(m)
		return p, cmd
	case silenceform.SubmittedMsg, silenceform.CancelledMsg, modal.ConfirmResultMsg, bulkExpireDoneMsg:
		_ = m // multi-type case: handleWriteResult consumes msg via its own type switch
		cmd := p.handleWriteResult(msg)
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
		p.scope = m.Scope
		p.recompute()
		return true, nil
	case app.TimeFormatChangedMsg:
		p.timeFormat = m.Format
		return true, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		p.snapshotFocus()
		return true, nil
	case app.ClearMarksMsg:
		return true, p.handleClearMarks()
	}
	return false, nil
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
	newCursor, handled := cursor.HandleMotion(
		m.String(),
		p.cursor,
		len(p.view),
		cursor.HalfPageStep(p.bodyHeight),
		cursor.FullPageStep(p.bodyHeight),
	)
	if !handled {
		return false
	}
	p.cursor = newCursor
	p.snapshotFocus()
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
		cmd := p.openNewSilenceForm()
		return p, cmd
	case "e":
		cmd := p.openEditSilenceForm()
		return p, cmd
	case "x":
		cmd := p.openExpireConfirmUnified()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "ctrl+e":
		cmd := p.openEditorForCursor()
		return p, cmd
	case "ctrl+n":
		cmd := p.openRecreateSilenceForm()
		return p, cmd
	case "r":
		cmd := p.requestRefresh()
		return p, cmd
	}
	return p, nil
}

// drillToDetail returns a Cmd that pushes the silence-detail page
// for the row under the cursor. Empty view falls through to a soft
// Info flash so the user sees a reason for the no-op.
func (p *Page) drillToDetail() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	entry := p.view[p.cursor]
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
func (p *Page) requestRefresh() tea.Cmd {
	p.refreshing = true
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "silences", Scope: scope}
	}
	return tea.Batch(emit, p.spinner.Tick)
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
	if p.cursor >= len(p.view) {
		return silenceform.Options{}, flashFn(footer.FlashInfo, "no silence under the cursor"), false
	}
	if len(p.clients) == 0 {
		return silenceform.Options{}, flashFn(footer.FlashWarn, hintNoWriteableBackend), false
	}
	entry := p.view[p.cursor]
	if entry.s.State != backend.SilenceStateExpired {
		return silenceform.Options{}, flashFn(footer.FlashInfo,
			"only expired silences can be recreated — use `e` to edit a live silence"), false
	}
	client, ok := p.clients[entry.tenant]
	if !ok {
		return silenceform.Options{}, flashFn(footer.FlashWarn, hintNoWriteableBackend), false
	}
	return silenceform.Options{
		Client:    client,
		Styles:    p.styles,
		Now:       p.now,
		Creator:   p.defaultCreator(),
		Matchers:  entry.s.Matchers,
		Comment:   entry.s.Comment,
		BlankEnds: true,
		FocusEnds: true,
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

// openEditorForCursor hands the cursor silence to the user's
// $EDITOR via the page's edit.Resolver. Captures the silence
// (id, tenant, snapshot) into p.pendingEdit so the FinishedMsg
// handler can route UpdateSilence at the right backend after the
// editor returns.
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
	creator := p.defaultCreator()
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

// flashFn returns a Cmd that emits a FlashShowMsg with the
// supplied level and text. Tiny indirection so the page's
// action handlers stay one-liners and so the level (Info / Warn
// / Error / Success) reads at the call site.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

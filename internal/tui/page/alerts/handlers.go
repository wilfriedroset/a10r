// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"log/slog"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/alert"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/groupdetail"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/poll"

	"charm.land/bubbles/v2/spinner"
)

func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	if handled, cmd := p.HandleSidebandMsg(msg); handled {
		return p, cmd
	}
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		p.HandleBackendStatusMsg(m)
		return p, nil
	case poll.DataMsg:
		listpage.ApplyDataMsg(&p.Base, &p.PollingUI, m, func(tenant string, alerts []backend.Alert) {
			p.byTenant[tenant] = alerts
		})
		return p, nil
	case spinner.TickMsg:
		// Drop ticks outside the cold-start / refresh-in-flight
		// windows to break the self-perpetuating Tick chain when
		// nothing is loading.
		if !p.SpinnerActive(p.ScopeIncludes) {
			return p, nil
		}
		var cmd tea.Cmd
		p.Spinner, cmd = p.Spinner.Update(m)
		return p, cmd
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.HandleFilterPrompt(m)
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash the new silence ID so the
		// user has confirmation. Same shape the silences page uses.
		slog.Default().Info("silence write succeeded",
			slog.String("op", "created"),
			slog.String("id", m.ID),
			slog.String("surface", "alerts-form"))
		return p, footer.ShowFlash(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. Esc on the form is a non-event.
		// Drop any pending round (bulk or single-cursor silence-all)
		// so a subsequent `s` doesn't reuse a stale target.
		p.pendingBulkSilence = pendingBulkSilence{}
		p.pendingSilenceAll = pendingSilenceAll{}
		return p, nil
	case silenceform.BulkSubmittedMsg:
		cmd := p.handleBulkSilenceSubmit(m)
		return p, cmd
	case modal.ConfirmResultMsg:
		cmd := p.handleConfirmResult(m)
		return p, cmd
	case bulkop.DoneMsg[string]:
		cmd := p.handleBulkSilenceDone(m)
		return p, cmd
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleKey processes vim-motion and per-view keys. Returns the
// page (possibly mutated) plus a Cmd. Split across handleMotion /
// handleSort / handleAction to keep each handler under cyclop=15.
func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleMotion(m) {
		return p, nil
	}
	if p.handleSort(m) {
		return p, nil
	}
	return p.handleAction(m)
}

// handleMotion processes cursor-walk keys. Returns true when the
// key was a motion so the caller stops the keymap walk.
func (p *Page) handleMotion(m tea.KeyPressMsg) bool {
	changed, handled := p.MoveCursor(m.String(), len(p.groups))
	if !handled {
		return false
	}
	if changed {
		p.snapshotFocus()
	}
	return true
}

// handleSort processes sort-column shortcuts (h/l walk plus
// Shift+letter direct shortcuts). Returns true when the key was
// a sort change.
//
// Direction semantics: pressing the active column's shortcut
// again flips ASC/DESC; pressing a different column's shortcut
// resets to that column's default direction. h/l walk also
// resets to default for the new column. This matches the spreadsheet-
// style "click again to invert" UX users expect.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	return cursor.HandleSort(
		m.String(),
		p.sorter,
		func() { p.focusGroupKey = "" },
		p.recompute,
	)
}

// handleAction processes the page's per-view action keys
// (Enter drill, Space mark, state-filter cycle, silence).
// Returns the page plus optional Cmd. Unrecognised keys are
// no-ops at this layer; the App's dispatcher had its turn
// earlier.
//
// State-filter cycling is bound to Shift+F (not `t`) because `t`
// is the app-global time-format toggle — the dispatcher's global
// `t` consumes the key before the page sees it, so a local `t`
// handler here would be dead code. bubbletea v2's
// KeyPressMsg.String() emits the textual form ("F") for shift-
// modified letters — never "shift+f" — so a single `case "F"`
// is sufficient.
func (p *Page) handleAction(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	switch m.String() {
	case "enter":
		cmd := p.drillToDetail()
		return p, cmd
	case "space":
		p.toggleMarkAtCursor()
	case "F":
		p.cycleStateFilter()
		p.recompute()
	case "T":
		// Ask the App to flip the app-global state-format density; the
		// page's SetStateFormat hook receives the broadcast result.
		return p, func() tea.Msg { return app.StateFormatToggleMsg{} }
	case "s":
		if p.readOnly {
			return p, footer.ShowFlash(footer.FlashWarn, hintReadOnly)
		}
		cmd := p.openSilenceForS()
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
func (p *Page) requestRefresh() tea.Cmd {
	return listpage.RequestRefresh(&p.Base, &p.PollingUI, resourceAlerts)
}

// openSilenceForS is the entry point for the `s` key — silence-all at
// L1. No marks → silence the cursor group: count==1 pushes the form
// directly, count>1 opens a blast-radius confirm modal first (CONTEXT
// "Silence-all"). With marks → the bulk silence-all fanout (one
// alertname silence per marked group). The single-cursor confirm and
// the ≥2-marks bulk confirm are distinct paths with separate pending
// state — see bulk.go.
func (p *Page) openSilenceForS() tea.Cmd {
	if len(p.marks) == 0 {
		return p.openSilenceAllForCursor()
	}
	return p.openBulkSilence()
}

// openSilenceAllForCursor silences every instance of the cursor
// group's alertname. Configuration errors win over view-state errors:
// an empty / missing-tenant Clients map flashes the actionable
// "no writeable backend" hint even on a cold-start empty view. A
// COUNT>1 group routes through a confirm modal (blast-radius gate);
// COUNT==1 pushes the form directly. The prefilled matcher is the
// group's identity — `alertname=<X>` alone, NOT the full label set.
func (p *Page) openSilenceAllForCursor() tea.Cmd {
	if len(p.clients) == 0 {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	if p.Index() >= len(p.groups) {
		return footer.ShowFlash(footer.FlashInfo, "no alert under the cursor")
	}
	g := p.groups[p.Index()]
	if _, ok := p.clients[g.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
	}
	p.pendingSilenceAll = pendingSilenceAll{
		tenant:    g.tenant,
		alertName: g.alertName,
		scopeNote: p.silenceAllScopeNote(g),
	}
	if g.count > 1 {
		return app.OpenModal(func() modal.Modal {
			return modal.NewConfirm(silenceAllQuestion(g), modal.ConfirmDefaultYes)
		})
	}
	return p.pushSilenceAllForm()
}

// hintReadOnly is the flash text emitted on a Dangerous keypress
// when the page is in read-only mode. Singular noun keeps it under
// the 80-col footer width.
const hintReadOnly = "read-only mode — alerts cannot be silenced"

// handleClearMarks drops every mark on the page in response to
// the global Ctrl+\ binding. Flashes "marks cleared" when the
// pre-clear count was non-zero so the user sees confirmation;
// silently no-ops otherwise (no flash on a key that did nothing
// would be a poor affordance, but an unconditional flash on a
// page that never had marks would be surprising spam).
func (p *Page) handleClearMarks() tea.Cmd {
	if len(p.marks) == 0 {
		return nil
	}
	p.marks = map[string]struct{}{}
	return footer.ShowFlash(footer.FlashInfo, "marks cleared")
}

func (p *Page) toggleMarkAtCursor() {
	listpage.ToggleMarkAtCursor(p.groups, p.Index(), p.marks, func(g alertGroup) string { return g.key() })
}

// drillToDetail returns a Cmd that drills into the cursor group. A
// single-instance group skips the L2 group-detail page and pushes the
// L3 instance detail straight away (the common case; a one-row L2
// would be pure friction). A multi-instance group pushes the L2 group-
// detail instance list. Empty view falls through to a soft Info flash.
//
// All deps are threaded by reference so the pushed page's `s` / `S`
// hit the same backends the alerts list would. Same maps — pages share
// the wiring layer's authoritative copy.
func (p *Page) drillToDetail() tea.Cmd {
	if p.Index() >= len(p.groups) {
		return footer.ShowFlash(footer.FlashInfo, "no alert under the cursor")
	}
	g := p.groups[p.Index()]
	if g.count == 1 {
		return p.drillToInstance(g)
	}
	return p.drillToGroup(g)
}

// drillToInstance pushes the L3 single-instance detail for a
// COUNT==1 group's sole instance.
func (p *Page) drillToInstance(g alertGroup) tea.Cmd {
	return app.PushPage(func() app.Page { return p.buildInstancePage(g) })
}

// buildInstancePage constructs the L3 instance-detail page for a
// COUNT==1 group. Split from drillToInstance so the factory body is
// directly testable for the destination type without reaching into
// the App's unexported push message.
func (p *Page) buildInstancePage(g alertGroup) app.Page {
	return alert.New(alert.Options{
		Alert:           g.instances[0],
		Tenant:          g.tenant,
		Styles:          p.styles,
		Now:             p.now,
		Clients:         p.clients,
		Creator:         p.creator,
		TimeFormat:      p.timeFormat,
		ReadOnly:        p.readOnly,
		BulkConcurrency: p.bulkConcurrency,
		Logger:          p.logger,
		BulkCtx:         p.bulkCtx,
		SubmitCtx:       p.submitCtx,
		EditorResolver:  p.editorResolver,
		EditorCtx:       p.editorCtx,
	})
}

// drillToGroup pushes the L2 group-detail instance list for a
// multi-instance group, seeding it with the group's instances and the
// page's current time / state format so it opens in the same density
// the list showed.
func (p *Page) drillToGroup(g alertGroup) tea.Cmd {
	return app.PushPage(func() app.Page { return p.buildGroupPage(g) })
}

// buildGroupPage constructs the L2 group-detail page for a COUNT>1
// group. Split from drillToGroup for the same testability reason as
// buildInstancePage. The instances slice is copied so the pushed page
// never aliases the live group.
func (p *Page) buildGroupPage(g alertGroup) app.Page {
	return groupdetail.New(groupdetail.Options{
		Tenant:          g.tenant,
		AlertName:       g.alertName,
		Instances:       append([]backend.Alert(nil), g.instances...),
		Styles:          p.styles,
		Now:             p.now,
		Clients:         p.clients,
		Creator:         p.creator,
		TimeFormat:      p.timeFormat,
		StateFormat:     p.stateFormat,
		ReadOnly:        p.readOnly,
		BulkConcurrency: p.bulkConcurrency,
		Logger:          p.logger,
		BulkCtx:         p.bulkCtx,
		SubmitCtx:       p.submitCtx,
		EditorResolver:  p.editorResolver,
		EditorCtx:       p.editorCtx,
	})
}

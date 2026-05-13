// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"log/slog"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/alert"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
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
		// Drop status for tenants outside the configured list — a
		// wire-layer bug, test leak, or future hot-reload that hasn't
		// pruned its sources could otherwise pollute lastErrors with
		// names that will never poll again. Empty Tenants disables
		// the guard (test fixtures that don't pin the list).
		if !p.knownTenant(m.Tenant) {
			return p, nil
		}
		// Track per-tenant transport errors for the error band.
		// A successful transition (Detail empty) clears the row;
		// failure transitions overwrite with the latest detail
		// the operator should see.
		if m.Detail == "" {
			delete(p.lastErrors, m.Tenant)
		} else {
			p.lastErrors[m.Tenant] = m.Detail
		}
		return p, nil
	case poll.DataMsg:
		alerts, ok := m.Resource.([]backend.Alert)
		if !ok {
			return p, nil
		}
		// Same guard as BackendStatusMsg: refuse data from tenants
		// not in the configured list so byTenant/polledTenants don't
		// hold entries for names that will never be polled or rendered.
		if !p.knownTenant(m.Tenant) {
			return p, nil
		}
		// Watch-mode: paused pages drop the snapshot so the table
		// does not move under the cursor mid-read. A pending
		// pausedRefresh from a manual `r` press lets a single tick
		// through and clears itself, so the operator can pull
		// fresh data on demand without leaving paused state.
		if p.paused && !p.pausedRefresh {
			return p, nil
		}
		p.pausedRefresh = false
		p.byTenant[m.Tenant] = alerts
		// Capture poll metadata so Footer / Title can render without
		// a parallel ticker. Zero-valued NextAt (legacy / test
		// DataMsgs) leaves the prior entry intact.
		if !m.NextAt.IsZero() {
			p.nextRefresh[m.Tenant] = m.NextAt
		}
		p.polledTenants[m.Tenant] = struct{}{}
		// Only clear refreshing once an in-scope tenant has answered;
		// an out-of-scope reply during a manual `r` window would
		// otherwise drop the spinner before the user has actually
		// seen fresh data for the scope they're looking at.
		if p.scopeIncludes(m.Tenant) {
			p.refreshing = false
		}
		p.recompute()
		return p, nil
	case spinner.TickMsg:
		// Drop ticks outside the cold-start / refresh-in-flight
		// windows to break the self-perpetuating Tick chain when
		// nothing is loading.
		if !p.spinnerActive() {
			return p, nil
		}
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(m)
		return p, cmd
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.handleFilterPrompt(m)
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash the new silence ID so the
		// user has confirmation. Same shape the silences page uses.
		slog.Default().Info("silence write succeeded",
			slog.String("op", "created"),
			slog.String("id", m.ID),
			slog.String("surface", "alerts-form"))
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		// Auto-pop already happened. Esc on the form is a non-event.
		// If a pending bulk round was waiting for the form, drop it
		// so a subsequent `s` doesn't reuse a stale target list.
		p.pendingBulkSilence = pendingBulkSilence{}
		return p, nil
	case silenceform.BulkSubmittedMsg:
		cmd := p.handleBulkSilenceSubmit(m)
		return p, cmd
	case modal.ConfirmResultMsg:
		cmd := p.handleBulkSilenceConfirm(m)
		return p, cmd
	case bulkSilenceDoneMsg:
		cmd := p.handleBulkSilenceDone(m)
		return p, cmd
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
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
		p.recomputeScroll()
		return true, nil
	case app.ClearMarksMsg:
		return true, p.handleClearMarks()
	}
	return false, nil
}

// handleFilterPrompt centralises the four filter-prompt lifecycle
// messages so Update stays under the cyclop budget. Each branch:
//
//   - Opened: snapshot the active filter and clear it so the user
//     types against the unfiltered list (live filter rebuilds it
//     keystroke-by-keystroke).
//   - Changed: apply the in-flight value live; preFilter stays so
//     Esc can still roll back regardless of what's been typed.
//   - Submitted: commit the typed value (possibly empty, meaning
//     "clear the filter"); drop the pre-prompt snapshot.
//   - Cancelled: restore the snapshot.
//
// Command-mode prompt messages slip through unchanged — the alerts
// page only owns filter mode.
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
	p.recomputeScroll()
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
		func() { p.focusFingerprint = "" },
		p.recompute,
	)
}

// handleAction processes the page's per-view action keys
// (Enter drill, Space mark, state-filter cycle, silence).
// Returns the page plus optional Cmd. Unrecognised keys are
// no-ops at this layer; the App's dispatcher had its turn
// earlier.
//
// State-filter cycling is bound to Shift+F (not `t`) since `t`
// is the app-global time-format toggle as of #9 — the
// dispatcher's global `t` consumes the key before the page sees
// it, so a local `t` handler here would be dead code. bubbletea
// v2's KeyPressMsg.String() emits the textual form ("F") for
// shift-modified letters — never "shift+f" — so a single `case
// "F"` is sufficient.
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
	case "s":
		if p.readOnly {
			return p, flashFn(footer.FlashWarn, hintReadOnly)
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

// toggleWatch flips paused state. When un-pausing, also clear any
// pending pausedRefresh — the next DataMsg should be treated
// normally because the page is no longer paused. When pausing
// without an in-flight `r` press, leave pausedRefresh false so
// the next ordinary DataMsg is silently dropped.
func (p *Page) toggleWatch() {
	p.paused = !p.paused
	if !p.paused {
		p.pausedRefresh = false
	}
}

// requestRefresh emits a RefreshRequestedMsg so the wiring layer
// pokes the alerts pollers, flips the page into refreshing
// state, and (re)kicks the spinner Tick chain. Mirror of the
// silences page's helper.
//
// When paused, sets pausedRefresh so the next incoming DataMsg
// is honoured exactly once — the operator pulled it deliberately
// and expects to see fresh data even though watch mode is off.
func (p *Page) requestRefresh() tea.Cmd {
	p.refreshing = true
	if p.paused {
		p.pausedRefresh = true
	}
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "alerts", Scope: scope}
	}
	return tea.Batch(emit, p.spinner.Tick)
}

// openSilenceForS is the entry point for the `s` key. k9s-style:
// no marks → cursor row, single-form gate (existing wording);
// 1 mark → push the bulk form directly (the form is the gate at
// N=1 — a separate confirm would be redundant); ≥2 marks → confirm
// modal first, then bulk form. Mirror of the silences page's
// openExpireConfirmUnified shape.
func (p *Page) openSilenceForS() tea.Cmd {
	if len(p.marks) == 0 {
		return p.openSilenceFormForCursor()
	}
	return p.openBulkSilence()
}

// openSilenceFormForCursor pushes the silence form prefilled with
// the cursor alert's labels as matchers. Configuration errors win
// over view-state errors: an empty Clients map flashes the same
// "no writeable backend" hint even on a cold-start empty view, so
// a misconfigured user sees the actionable message first. The
// silenceform.MatchersFromLabels helper drops the synthetic
// `__name__` label — silencing on it would silence every alert
// carrying that metric name.
func (p *Page) openSilenceFormForCursor() tea.Cmd {
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no alert under the cursor")
	}
	entry := p.view[p.cursor]
	if _, ok := p.clients[entry.tenant]; !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	matchers := silenceform.MatchersFromLabels(entry.a.Labels)
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	tenant := entry.tenant
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:  clients,
			Tenant:   tenant,
			Styles:   styles,
			Now:      now,
			Creator:  creator,
			Matchers: matchers,
		})
	})
}

// hintReadOnly is the flash text emitted on a Dangerous keypress
// when the page is in read-only mode. Singular noun keeps it under
// the 80-col footer width.
const hintReadOnly = "read-only mode — alerts cannot be silenced"

func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

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
	return flashFn(footer.FlashInfo, "marks cleared")
}

// toggleMarkAtCursor flips the mark on the row under the cursor.
// No-op on an empty view. Empty fingerprints (alerts without a
// stable identifier) are silently skipped — there's no key to
// associate the mark with.
func (p *Page) toggleMarkAtCursor() {
	if p.cursor >= len(p.view) {
		return
	}
	fp := p.view[p.cursor].a.Fingerprint
	if fp == "" {
		return
	}
	if _, ok := p.marks[fp]; ok {
		delete(p.marks, fp)
		return
	}
	p.marks[fp] = struct{}{}
}

// drillToDetail returns a Cmd that pushes the alert-detail page
// for the row under the cursor. Empty view falls through to a
// soft Info flash so the user sees a reason for the no-op.
//
// Clients / Creator are threaded so the detail page's `s` push
// hits the same backend the alerts list `s` would. Same map by
// reference — pages share the wiring layer's authoritative copy.
func (p *Page) drillToDetail() tea.Cmd {
	if p.cursor >= len(p.view) {
		return flashFn(footer.FlashInfo, "no alert under the cursor")
	}
	entry := p.view[p.cursor]
	styles := p.styles
	now := p.now
	clients := p.clients
	creator := p.creator
	tf := p.timeFormat
	readOnly := p.readOnly
	return app.PushPage(func() app.Page {
		return alert.New(alert.Options{
			Alert:      entry.a,
			Tenant:     entry.tenant,
			Styles:     styles,
			Now:        now,
			Clients:    clients,
			Creator:    creator,
			TimeFormat: tf,
			ReadOnly:   readOnly,
		})
	})
}

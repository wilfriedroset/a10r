// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"log/slog"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/poll"

	"charm.land/bubbles/v2/spinner"
)

// Update implements app.Page.
func (p *Page) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	switch m := msg.(type) {
	case poll.BackendStatusMsg:
		p.HandleBackendStatusMsg(m)
		return p, nil
	case poll.DataMsg:
		groups, ok := m.Resource.([]backend.AlertGroup)
		if !ok {
			return p, nil
		}
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
		p.byTenant[m.Tenant] = groups
		if !m.NextAt.IsZero() {
			p.NextRefresh[m.Tenant] = m.NextAt
		}
		p.PolledTenants[m.Tenant] = struct{}{}
		if p.ScopeIncludes(m.Tenant) {
			p.Refreshing = false
		}
		p.recompute()
		return p, nil
	case spinner.TickMsg:
		if !p.spinnerActive() {
			return p, nil
		}
		var cmd tea.Cmd
		p.Spinner, cmd = p.Spinner.Update(m)
		return p, cmd
	case app.ScopeChangedMsg:
		p.HandleScopeChangedMsg(m)
		return p, nil
	case app.GoToFirstRowMsg:
		p.SetIndex(0, len(p.rows()))
		p.snapshotFocus()
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash so the user sees
		// confirmation. Same shape alerts / silences use.
		slog.Default().Info("silence write succeeded",
			slog.String("op", "created"),
			slog.String("id", m.ID),
			slog.String("surface", "groups-form"))
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		return p, nil
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.HandleFilterPrompt(m)
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleKey processes vim-motion + per-view keys. Returns the
// page (possibly mutated) plus a Cmd. Motion goes through the
// shared cursor.HandleMotion helper; sort cycle through
// p.handleSort; everything else falls into the action switch
// (tab toggle-all, enter, s, r).
func (p *Page) handleKey(m tea.KeyPressMsg) (app.Page, tea.Cmd) {
	if p.handleSort(m) {
		return p, nil
	}
	rows := p.rows()
	// `g` alone is dead code — the dispatcher's chord buffer at
	// LayerTable consumes the first `g` waiting for the second. The
	// chord-completed `gg` arrives as app.GoToFirstRowMsg and is
	// handled in Update.
	if changed, handled := p.MoveCursor(m.String(), len(rows)); handled {
		if changed {
			p.snapshotFocus()
		}
		return p, nil
	}
	switch m.String() {
	case "tab":
		p.toggleExpandAll()
		p.Clamp(len(p.rows()))
	case "enter":
		return p.onEnter(rows)
	case "s":
		if p.readOnly {
			return p, flashFn(footer.FlashWarn, hintReadOnly)
		}
		return p.onSilence(rows)
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

// handleSort processes sort-axis shortcuts (h/l walk plus
// Shift+letter direct shortcuts). Returns true when the key was a
// sort change so the caller skips its other branches. Same shape
// as alerts / silences so the three list pages stay aligned.
//
// `s` is the silence verb on this page, so the severity sort
// uses Shift+V (mnemonic for "severity"), avoiding a collision
// with the existing `s` action handler.
func (p *Page) handleSort(m tea.KeyPressMsg) bool {
	return cursor.HandleSort(
		m.String(),
		p.sorter,
		func() { p.focusKey = "" },
		p.recompute,
	)
}

// requestRefresh emits RefreshRequestedMsg and re-arms the
// spinner. Same shape as alerts / silences.
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
		return app.RefreshRequestedMsg{Resource: "groups", Scope: scope}
	}
	return tea.Batch(emit, p.Spinner.Tick)
}

// toggleExpandAll flips every group's expanded flag based on the
// current majority — if any group is collapsed, expand all;
// otherwise collapse all.
func (p *Page) toggleExpandAll() {
	wantExpand := false
	for _, e := range p.expanded {
		if !e {
			wantExpand = true
			break
		}
	}
	for i := range p.expanded {
		p.expanded[i] = wantExpand
	}
	p.cachedRows = nil
}

// onEnter expands / collapses a group header or drills to a leaf
// alert.
func (p *Page) onEnter(rows []row) (app.Page, tea.Cmd) {
	if p.Index() >= len(rows) {
		return p, nil
	}
	r := rows[p.Index()]
	if r.alertIdx == -1 {
		p.expanded[r.groupIdx] = !p.expanded[r.groupIdx]
		p.cachedRows = nil
		p.Clamp(len(p.rows()))
		return p, nil
	}
	alert := p.flat[r.groupIdx].g.Alerts[r.alertIdx]
	return p, func() tea.Msg { return DrillAlertMsg{Alert: alert} }
}

// onSilence pushes the silence form prefilled with the cursor
// group's common-labels intersection (`__name__` dropped). The
// cursor on a leaf row still uses the leaf's parent group — so
// silencing a single alert requires drilling into it first via
// Enter, then `s` on the detail page; `s` from the groups view
// always covers every alert in the group.
func (p *Page) onSilence(rows []row) (app.Page, tea.Cmd) {
	if p.Index() >= len(rows) {
		return p, flashFn(footer.FlashInfo, "no group under the cursor")
	}
	r := rows[p.Index()]
	if r.groupIdx >= len(p.flat) {
		return p, flashFn(footer.FlashInfo, "no group under the cursor")
	}
	entry := p.flat[r.groupIdx]
	if len(p.clients) == 0 {
		return p, flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	if _, ok := p.clients[entry.tenant]; !ok {
		return p, flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	matchers := silenceform.MatchersFromLabels(commonLabels(entry.g.Alerts))
	if len(matchers) == 0 {
		// An empty matcher list submitted to alertmanager silences
		// EVERY alert. Refuse to push the form rather than let the
		// operator unknowingly arm a fleet-wide silence; common
		// triggers are a group whose backend rebalanced its alerts
		// away mid-poll, or a heterogeneous group with no
		// labels-in-common. Drill into an individual alert and
		// silence from there if that's the intent.
		return p, flashFn(footer.FlashError,
			"cannot silence group with no common labels — drill into an alert and silence it individually")
	}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	tenant := entry.tenant
	submitCtx := p.submitCtx
	return p, app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:   clients,
			Tenant:    tenant,
			Styles:    styles,
			Now:       now,
			Creator:   creator,
			Matchers:  matchers,
			SubmitCtx: submitCtx,
		})
	})
}

// flashFn returns a Cmd emitting a FlashShowMsg. Tiny helper so
// the action handlers stay one-liners.
func flashFn(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

// hintNoWriteableBackend mirrors the alerts / alert page consts
// so the "configure a writeable backend" hint reads identically
// across the three pages that push the silence form on `s`.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

// hintReadOnly is the flash text emitted when `s` fires on a
// read-only page. Same intent as silences/alerts but worded for
// the groups affordance ("silence group").
const hintReadOnly = "read-only mode — groups cannot be silenced"

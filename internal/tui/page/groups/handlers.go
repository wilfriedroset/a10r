// SPDX-License-Identifier: Apache-2.0

package groups

import (
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
	case poll.DataMsg:
		groups, ok := m.Resource.([]backend.AlertGroup)
		if !ok {
			return p, nil
		}
		p.byTenant[m.Tenant] = groups
		if !m.NextAt.IsZero() {
			p.nextRefresh[m.Tenant] = m.NextAt
		}
		p.polledTenants[m.Tenant] = struct{}{}
		if p.scopeIncludes(m.Tenant) {
			p.refreshing = false
		}
		p.recompute()
		return p, nil
	case spinner.TickMsg:
		if !p.spinnerActive() {
			return p, nil
		}
		var cmd tea.Cmd
		p.spinner, cmd = p.spinner.Update(m)
		return p, cmd
	case app.ScopeChangedMsg:
		p.scope = m.Scope
		p.recompute()
		return p, nil
	case app.GoToFirstRowMsg:
		p.cursor = 0
		p.snapshotFocus()
		p.recomputeScroll()
		return p, nil
	case silenceform.SubmittedMsg:
		// Form auto-popped already; flash so the user sees
		// confirmation. Same shape alerts / silences use.
		return p, flashFn(footer.FlashSuccess, "silence created: "+m.ID)
	case silenceform.CancelledMsg:
		return p, nil
	case footer.PromptOpenedMsg, footer.PromptChangedMsg,
		footer.PromptSubmittedMsg, footer.PromptCancelledMsg:
		p.handleFilterPrompt(m)
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// handleFilterPrompt centralises the four filter-prompt lifecycle
// messages (Opened / Changed / Submitted / Cancelled) so Update
// stays under the cyclop budget. Same shape as alerts / silences;
// see those pages for the per-branch contract.
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
			p.clampCursor()
		}
	case footer.PromptChangedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		p.filter = m.Value
		p.clampCursor()
	case footer.PromptSubmittedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		p.filter = m.Value
		p.preFilter = nil
		p.clampCursor()
	case footer.PromptCancelledMsg:
		if m.Mode != footer.PromptFilter || p.preFilter == nil {
			return
		}
		p.filter = *p.preFilter
		p.preFilter = nil
		p.clampCursor()
	}
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
	if newCursor, handled := cursor.HandleMotion(
		m.String(),
		p.cursor,
		len(rows),
		cursor.HalfPageStep(p.bodyHeight),
		cursor.FullPageStep(p.bodyHeight),
	); handled {
		p.cursor = newCursor
		p.snapshotFocus()
		p.recomputeScroll()
		return p, nil
	}
	switch m.String() {
	case "tab":
		p.toggleExpandAll()
		p.recomputeScroll()
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
	}
	return p, nil
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
func (p *Page) requestRefresh() tea.Cmd {
	p.refreshing = true
	scope := p.scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: "groups", Scope: scope}
	}
	return tea.Batch(emit, p.spinner.Tick)
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
}

// onEnter expands / collapses a group header or drills to a leaf
// alert.
func (p *Page) onEnter(rows []row) (app.Page, tea.Cmd) {
	if p.cursor >= len(rows) {
		return p, nil
	}
	r := rows[p.cursor]
	if r.alertIdx == -1 {
		p.expanded[r.groupIdx] = !p.expanded[r.groupIdx]
		p.recomputeScroll()
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
	if p.cursor >= len(rows) {
		return p, flashFn(footer.FlashInfo, "no group under the cursor")
	}
	r := rows[p.cursor]
	if r.groupIdx >= len(p.flat) {
		return p, flashFn(footer.FlashInfo, "no group under the cursor")
	}
	entry := p.flat[r.groupIdx]
	if len(p.clients) == 0 {
		return p, flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	client, ok := p.clients[entry.tenant]
	if !ok {
		return p, flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	matchers := silenceform.MatchersFromLabels(commonLabels(entry.g.Alerts))
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	return p, app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Client:   client,
			Styles:   styles,
			Now:      now,
			Creator:  creator,
			Matchers: matchers,
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

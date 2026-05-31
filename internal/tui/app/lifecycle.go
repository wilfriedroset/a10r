// SPDX-License-Identifier: Apache-2.0

package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// Update implements tea.Model. Routes the message to whichever
// subcomponent owns it; falls back to the dispatcher for plain key events.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, handled := a.handleLifecycle(msg); handled {
		return a, cmd
	}
	if cmd, handled := a.handleInput(msg); handled {
		return a, cmd
	}
	if _, ok := msg.(help.ClosedMsg); ok {
		// Own branch, not the modal.ResultMsg fan-out: viewer overlays
		// don't satisfy ResultMsg (ADR 0020) and no page observes the close.
		a.overlays.help = nil
		return a, nil
	}
	if isModalResult(msg) {
		a.closeModal()
		// Scope-origin (Ctrl+T) submissions become a global
		// ScopeChangedMsg; other Origins fall through to the page that
		// opened the picker. Empty and "all" both resolve to scope "all".
		if pm, ok := msg.(modal.PickerSubmittedMsg); ok && pm.Origin == PickerOriginScope {
			scope := pickerSelectionsToScope(pm.Selections, a.tenants)
			return a, func() tea.Msg { return ScopeChangedMsg{Scope: scope} }
		}
		if pc, ok := msg.(modal.PickerCancelledMsg); ok && pc.Origin == PickerOriginScope {
			// Cancelling the global scope picker is a no-op; other Origins
			// fall through so the originator can react.
			return a, nil
		}
		cmd := a.forwardToTop(msg)
		return a, cmd
	}
	if _, ok := msg.(AutoPopMsg); ok {
		// Pop the form first so the parent is on top, then forward so the
		// parent can react. Symmetrical with the modal-result path above.
		closeCmd := a.popPage()
		fwdCmd := a.forwardToTop(msg)
		return a, tea.Batch(closeCmd, fwdCmd)
	}
	if a.flash.Owns(msg) {
		var cmd tea.Cmd
		a.flash, cmd = a.flash.Update(msg)
		return a, cmd
	}
	if a.hintbar.Owns(msg) {
		var cmd tea.Cmd
		a.hintbar, cmd = a.hintbar.Update(msg)
		return a, cmd
	}
	cmd := a.forwardToTop(msg)
	return a, cmd
}

// handleLifecycle covers the App's own message types, returning
// (cmd, true) when handled. It fans out into three clusters (session,
// poll snapshotting, stack/modal) so each switch stays small.
func (a *App) handleLifecycle(msg tea.Msg) (tea.Cmd, bool) {
	if cmd, handled := a.handleSessionMsg(msg); handled {
		return cmd, true
	}
	if cmd, handled := a.handlePollMsg(msg); handled {
		return cmd, true
	}
	return a.handleStackMsg(msg)
}

// handleSessionMsg covers session-level lifecycle messages: window
// resize, quit, chord-timer expiry, and refresh requests.
func (a *App) handleSessionMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a.forwardToTop(m), true
	case tea.QuitMsg:
		a.quitting = true
		return nil, true
	case QuitRequestedMsg:
		// Page-stack tear-down before tea.Quit; see QuitRequestedMsg's doc.
		return a.quitWithCleanup(), true
	case keys.ChordExpiredMsg:
		return a.dispatcher.HandleChordExpired(m), true
	case StateFormatToggleMsg:
		// The App owns the canonical density and broadcasts the flip so a
		// page below the stack during an earlier toggle stays in step.
		return a.toggleStateFormatCmd(), true
	case RefreshRequestedMsg:
		// Nil handler (headless tests, no-config wizard) no-ops so an early
		// `r` press doesn't crash.
		if a.refresh != nil {
			a.refresh(m.Resource, m.Scope)
		}
		return nil, true
	}
	return nil, false
}

// handlePollMsg snapshots poll payloads before forwarding to the top page,
// so a page pushed later in the same tick hydrates without waiting a full
// poll interval.
//
// DataMsg: the cache write fires only when ResourceLabel is stamped;
// unlabelled DataMsgs (tests) just forward. BackendStatusMsg is emitted
// only on state changes, so the per-tenant snapshot lets a page landing
// mid-outage still light the band; empty Detail prunes the entry.
func (a *App) handlePollMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case poll.DataMsg:
		if m.ResourceLabel != "" {
			a.cacheDataMsg(m)
		}
		return a.forwardToTop(m), true
	case poll.BackendStatusMsg:
		a.cacheStatusMsg(m)
		return a.forwardToTop(m), true
	}
	return nil, false
}

// handleStackMsg routes page-stack and modal-slot operations
// emitted by key handlers (PushPage, PopPage, ReplacePage,
// OpenModal, CloseModal, OpenHelp).
func (a *App) handleStackMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case pushPageMsg:
		return a.pushPage(m.Factory), true
	case popPageMsg:
		return a.popPage(), true
	case replacePageMsg:
		return a.replacePage(m.Factory), true
	case openModalMsg:
		return a.openModal(m.Factory), true
	case closeModalMsg:
		a.closeModal()
		return nil, true
	case openHelpMsg:
		a.openHelp(m.Options)
		return nil, true
	}
	return nil, false
}

// forwardToTop delivers msg to the top-of-stack page, swapping in the
// derivative it returns. Returns nil when the stack is empty.
func (a *App) forwardToTop(msg tea.Msg) tea.Cmd {
	if len(a.stack) == 0 {
		return nil
	}
	top, cmd := a.stack[len(a.stack)-1].Update(msg)
	a.stack[len(a.stack)-1] = top
	a.refreshCrumbs()
	return cmd
}

// pushPage adds a new page on top, runs its Init, refreshes crumbs, and
// replays cached poll snapshots so it hydrates without waiting for the
// next tick. Returns the batched Init and replay Cmds.
func (a *App) pushPage(factory func() Page) tea.Cmd {
	if factory == nil {
		return nil
	}
	page := factory()
	if page == nil {
		return nil
	}
	a.stack = append(a.stack, page)
	a.refreshCrumbs()
	initCmd := page.Init()
	replayCmd := a.replayCachedDataMsgs()
	return tea.Batch(initCmd, replayCmd)
}

// quitWithCleanup walks the page stack top-first invoking Close() to
// cancel background work, then batches a terminating tea.Quit. An empty
// stack still emits tea.Quit so a cold-start quit exits cleanly.
//
// CRITICAL: a.quitting is flipped HERE, not in handleLifecycle's
// `case tea.QuitMsg` (dead code in production — bubbletea's eventLoop
// catches tea.QuitMsg before dispatching to Update). The terminating
// tea.Quit emits a QuitMsg the wiring-layer filter (cmd/tui.go's
// newQuitFilter) inspects via a.Quitting(); without the flip the filter
// would rewrite QuitMsg back to QuitRequestedMsg forever and never exit.
func (a *App) quitWithCleanup() tea.Cmd {
	a.quitting = true
	cmds := make([]tea.Cmd, 0, len(a.stack)+1)
	for _, page := range slices.Backward(a.stack) {
		if c := page.Close(); c != nil {
			cmds = append(cmds, c)
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
}

// popPage removes the top page when the stack has more than one entry,
// calling Close so its background work winds down. Popping the last page
// is a no-op so the home view always stays visible.
func (a *App) popPage() tea.Cmd {
	if len(a.stack) <= 1 {
		return nil
	}
	departing := a.stack[len(a.stack)-1]
	a.stack = a.stack[:len(a.stack)-1]
	a.refreshCrumbs()
	return departing.Close()
}

// replacePage swaps the top page for the factory's output, sequencing the
// displaced page's Close before the new page's Init so teardown finishes
// first. An empty stack falls back to push.
func (a *App) replacePage(factory func() Page) tea.Cmd {
	if factory == nil {
		return nil
	}
	if len(a.stack) == 0 {
		return a.pushPage(factory)
	}
	page := factory()
	if page == nil {
		return nil
	}
	departing := a.stack[len(a.stack)-1]
	a.stack[len(a.stack)-1] = page
	a.refreshCrumbs()
	cmd := tea.Sequence(departing.Close(), page.Init())
	replayCmd := a.replayCachedDataMsgs()
	return tea.Batch(cmd, replayCmd)
}

// cacheDataMsg stores the latest DataMsg per (ResourceLabel, Tenant);
// later ticks overwrite, since the cache is a snapshot not a history.
func (a *App) cacheDataMsg(m poll.DataMsg) {
	bucket := a.caches.poll[m.ResourceLabel]
	if bucket == nil {
		bucket = map[string]poll.DataMsg{}
		a.caches.poll[m.ResourceLabel] = bucket
	}
	bucket[m.Tenant] = m
}

// cacheStatusMsg stores the latest BackendStatusMsg per tenant. Empty
// Detail is a recovery transition: pruning keeps the cache aligned with
// the per-page delete-on-empty-Detail error-band semantics.
func (a *App) cacheStatusMsg(m poll.BackendStatusMsg) {
	if m.Detail == "" {
		delete(a.caches.status, m.Tenant)
		return
	}
	a.caches.status[m.Tenant] = m
}

// replayCachedDataMsgs feeds cached snapshots into the top page so it
// builds its byTenant map without waiting for the next poll tick. Poll
// payloads are label-filtered for PollAwarePage pages (others get all).
// Returned Cmds are batched so a future DataMsg→Cmd page doesn't lose its
// kick. BackendStatusMsg is replayed unconditionally: filtering it by
// PollResources would couple the error-band wiring to an unrelated label.
func (a *App) replayCachedDataMsgs() tea.Cmd {
	if len(a.stack) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	keep := a.replayLabelFilter()
	for label, bucket := range a.caches.poll {
		if !keep(label) {
			continue
		}
		for _, m := range bucket {
			cmds = a.dispatchAppend(cmds, m)
		}
	}
	for _, m := range a.caches.status {
		cmds = a.dispatchAppend(cmds, m)
	}
	return tea.Batch(cmds...)
}

// dispatchAppend feeds m to the top page, swaps in its derivative, and
// appends any non-nil command to cmds.
func (a *App) dispatchAppend(cmds []tea.Cmd, m tea.Msg) []tea.Cmd {
	top, cmd := a.stack[len(a.stack)-1].Update(m)
	a.stack[len(a.stack)-1] = top
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	return cmds
}

// replayLabelFilter returns a predicate for which cached poll labels to
// replay: a PollAwarePage page replays only its declared resources (empty
// = none); a page that doesn't implement it replays everything.
func (a *App) replayLabelFilter() func(label string) bool {
	pa, ok := a.stack[len(a.stack)-1].(PollAwarePage)
	if !ok {
		return func(string) bool { return true }
	}
	allowed := make(map[string]struct{}, len(pa.PollResources()))
	for _, l := range pa.PollResources() {
		allowed[l] = struct{}{}
	}
	return func(label string) bool {
		_, ok := allowed[label]
		return ok
	}
}

// refreshCrumbs rebuilds the breadcrumb strip from the current
// stack. Cheap on every frame because Crumbs.Set already does a
// defensive copy.
func (a *App) refreshCrumbs() {
	labels := make([]string, len(a.stack))
	for i, p := range a.stack {
		labels[i] = p.Crumb()
	}
	a.crumbs = a.crumbs.Set(labels)
}

// topPage returns the active page, or nil when the stack is empty.
func (a *App) topPage() Page {
	if len(a.stack) == 0 {
		return nil
	}
	return a.stack[len(a.stack)-1]
}

// activeViewLabel returns the top page's crumb (used by the help
// overlay to label its per-view section). Empty when no page is
// pushed.
func (a *App) activeViewLabel() string {
	if p := a.topPage(); p != nil {
		return p.Crumb()
	}
	return ""
}

// topPageCapturesInput reports whether the top-of-stack page is
// in raw-key-capture mode — a form or future text-entry page
// that wants every keystroke routed past the dispatcher.
func (a *App) topPageCapturesInput() bool {
	p, ok := a.topPage().(InputCapturePage)
	return ok && p.CapturesInput()
}

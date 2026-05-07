// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// Update implements tea.Model. Routes the message to whichever
// subcomponent owns it; falls back to the dispatcher for plain key
// events. Returns the model verbatim and the resulting command.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, handled := a.handleLifecycle(msg); handled {
		return a, cmd
	}
	if cmd, handled := a.handleInput(msg); handled {
		return a, cmd
	}
	if isModalResult(msg) {
		a.closeModal()
		// Tenant picker submissions translate into a global
		// ScopeChangedMsg so every page reacts the same way as for
		// the `<0>` / `<1>`-`<9>` quick-switch. Empty selection
		// (no marks → falls through to the cursor row, which we
		// treat as "all") and the literal "all" selection both
		// resolve to scope=="all".
		if pm, ok := msg.(modal.PickerSubmittedMsg); ok {
			scope := pickerSelectionsToScope(pm.Selections, a.tenants)
			return a, func() tea.Msg { return ScopeChangedMsg{Scope: scope} }
		}
		cmd := a.forwardToTop(msg)
		return a, cmd
	}
	if _, ok := msg.(AutoPopMsg); ok {
		// Forms emit submitted / cancelled messages tagged with
		// AutoPopMsg. The App pops the form off the stack first so
		// the parent page is on top, then forwards the message so
		// the parent can react (success flash, list refresh, …).
		// Symmetrical with the modal-result path above.
		closeCmd := a.popPage()
		fwdCmd := a.forwardToTop(msg)
		return a, tea.Batch(closeCmd, fwdCmd)
	}
	if a.flash.Owns(msg) {
		var cmd tea.Cmd
		a.flash, cmd = a.flash.Update(msg)
		return a, cmd
	}
	cmd := a.forwardToTop(msg)
	return a, cmd
}

// handleLifecycle covers the App's own message types: window
// resize, quit, chord-timer, page stack ops, modal slot ops.
// Returns (cmd, true) when handled.
func (a *App) handleLifecycle(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a.forwardToTop(m), true
	case tea.QuitMsg:
		a.quitting = true
		return nil, true
	case keys.ChordExpiredMsg:
		return a.dispatcher.HandleChordExpired(m), true
	case poll.DataMsg:
		// Snapshot before forwarding so a page push that lands
		// later — e.g. via PushPage from a key handler running in
		// the same Update tick — sees the freshest payload. The
		// labelled-cache write only fires when the poll layer
		// stamped ResourceLabel; legacy DataMsgs from tests skip
		// the cache and just forward.
		if m.ResourceLabel != "" {
			a.cacheDataMsg(m)
		}
		return a.forwardToTop(m), true
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
	case RefreshRequestedMsg:
		// Translate page-level refresh requests into poller nudges
		// via the wiring-layer handler. Nil-handler runs (headless
		// tests, no-config wizard) silently no-op so an early `r`
		// press doesn't crash. The page is free to surface a flash
		// of its own — the App stays out of UX feedback for refresh.
		if a.refresh != nil {
			a.refresh(m.Resource, m.Scope)
		}
		return nil, true
	}
	return nil, false
}

// forwardToTop delivers msg to the top-of-stack page (if any). The
// page is value-typed in the stack so the new derivative replaces
// the slot in-place. Returns the page's Cmd or nil when the stack
// is empty.
func (a *App) forwardToTop(msg tea.Msg) tea.Cmd {
	if len(a.stack) == 0 {
		return nil
	}
	top, cmd := a.stack[len(a.stack)-1].Update(msg)
	a.stack[len(a.stack)-1] = top
	a.refreshCrumbs()
	return cmd
}

// pushPage adds a new page on top, runs its Init, and refreshes the
// crumb strip. Returns the page's Init Cmd so callers can chain
// follow-ups (e.g. an alerts page kicking off a poll). After Init,
// cached poll snapshots are replayed into the new page so it
// hydrates from the freshest data immediately rather than waiting
// up to a full poll interval for the next tick.
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
	a.replayCachedDataMsgs()
	return initCmd
}

// popPage removes the top page when the stack has more than one
// entry, calling Close on it so pollers and other background work
// can wind down. Popping the last page is a no-op so the home view
// always stays visible — a keybindings.md "Esc on home view does
// nothing" is friendlier than ejecting the user into a black screen.
func (a *App) popPage() tea.Cmd {
	if len(a.stack) <= 1 {
		return nil
	}
	departing := a.stack[len(a.stack)-1]
	a.stack = a.stack[:len(a.stack)-1]
	a.refreshCrumbs()
	return departing.Close()
}

// replacePage swaps the top page for the factory's output. The
// displaced page's Close runs so its background work tears down
// before the new page's Init starts; the two Cmds run sequentially
// to keep that ordering deterministic. When the stack is empty,
// replacePage falls back to push so a user can always launch a
// fresh view from a no-page state.
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
	a.replayCachedDataMsgs()
	return cmd
}

// cacheDataMsg stores the latest DataMsg per (ResourceLabel,
// Tenant) tuple. Subsequent ticks for the same tuple overwrite —
// the cache is a snapshot, not a history.
func (a *App) cacheDataMsg(m poll.DataMsg) {
	bucket := a.pollCache[m.ResourceLabel]
	if bucket == nil {
		bucket = map[string]poll.DataMsg{}
		a.pollCache[m.ResourceLabel] = bucket
	}
	bucket[m.Tenant] = m
}

// replayCachedDataMsgs feeds cached snapshots into the top-of-
// stack page through its Update so a freshly-pushed page can
// build its byTenant map without waiting for the next poll tick.
//
// Replay is filtered by resource label when the top page
// implements PollAwarePage: only labels the page declared via
// PollResources() are replayed, so a silences page push doesn't
// re-walk the alerts / receivers / groups cache. Pages that don't
// implement the interface receive every cached payload — they
// already type-assert and ignore wrong shapes, so nothing breaks;
// the filter just trims the noise for opted-in pages.
//
// A returned Cmd is dropped: pages don't return Cmds in response
// to a poll DataMsg (verified by every existing page's Update
// branch). Should that invariant change, lift the helper to
// return tea.Batch of every page Cmd.
//
// The inner-loop write to a.stack[len-1] is safe because the App's
// Update path is single-threaded by bubbletea — replay runs
// inside the same Update call that pushed the page, so no other
// goroutine reads or writes the stack while this loop runs.
func (a *App) replayCachedDataMsgs() {
	if len(a.stack) == 0 {
		return
	}
	allowed, filtering := a.replayFilter()
	for label, bucket := range a.pollCache {
		if filtering {
			if _, ok := allowed[label]; !ok {
				continue
			}
		}
		for _, m := range bucket {
			top, _ := a.stack[len(a.stack)-1].Update(m)
			a.stack[len(a.stack)-1] = top
		}
	}
}

// replayFilter resolves the active page's PollResources()
// declaration into a lookup set. Returns (set, true) when the
// page opts in (an empty set means "want nothing" — still
// filtering); (nil, false) when the page doesn't implement
// PollAwarePage, in which case the caller skips filtering
// entirely.
func (a *App) replayFilter() (map[string]struct{}, bool) {
	pa, ok := a.stack[len(a.stack)-1].(PollAwarePage)
	if !ok {
		return nil, false
	}
	labels := pa.PollResources()
	allowed := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		allowed[l] = struct{}{}
	}
	return allowed, true
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

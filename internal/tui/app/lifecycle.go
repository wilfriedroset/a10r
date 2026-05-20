// SPDX-License-Identifier: Apache-2.0

package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/help"
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
	if _, ok := msg.(help.ClosedMsg); ok {
		// Help close runs through its own branch rather than the
		// modal.ResultMsg fan-out: viewer overlays don't satisfy
		// ResultMsg (per ADR 0020), and no page needs to observe
		// the dismissal — the overlay only renders information.
		a.help = nil
		return a, nil
	}
	if isModalResult(msg) {
		a.closeModal()
		// Tenant picker submissions tagged with the scope origin
		// (Ctrl+T) translate into a global ScopeChangedMsg so every
		// page reacts the same way as for the `<0>` / `<1>`-`<9>`
		// quick-switch. Pickers opened by pages — e.g. the silence
		// form's tenant row — carry a different Origin and fall
		// through to forwardToTop so the page Update consumes them.
		// Empty selection (no marks → falls through to the cursor
		// row, which we treat as "all") and the literal "all"
		// selection both resolve to scope=="all".
		if pm, ok := msg.(modal.PickerSubmittedMsg); ok && pm.Origin == PickerOriginScope {
			scope := pickerSelectionsToScope(pm.Selections, a.tenants)
			return a, func() tea.Msg { return ScopeChangedMsg{Scope: scope} }
		}
		if pc, ok := msg.(modal.PickerCancelledMsg); ok && pc.Origin == PickerOriginScope {
			// Cancelling the global scope picker is a no-op; the page
			// doesn't need to see it. Pickers with any other Origin
			// fall through so the originator can react.
			return a, nil
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
	if a.hintbar.Owns(msg) {
		var cmd tea.Cmd
		a.hintbar, cmd = a.hintbar.Update(msg)
		return a, cmd
	}
	cmd := a.forwardToTop(msg)
	return a, cmd
}

// handleLifecycle covers the App's own message types: window
// resize, quit, chord-timer, page stack ops, modal slot ops.
// Returns (cmd, true) when handled. The dispatch fans out into
// three clusters so each switch stays small and the conceptual
// grouping (session lifecycle vs poll snapshotting vs stack/modal
// ops) is explicit.
func (a *App) handleLifecycle(msg tea.Msg) (tea.Cmd, bool) {
	if cmd, handled := a.handleSessionMsg(msg); handled {
		return cmd, true
	}
	if cmd, handled := a.handlePollMsg(msg); handled {
		return cmd, true
	}
	return a.handleStackMsg(msg)
}

// handleSessionMsg covers session-level lifecycle messages:
// window resize, quit (both the bubbletea-native QuitMsg and the
// App's QuitRequestedMsg precursor that runs page Close()s first),
// chord-timer expiry, and refresh requests forwarded to the wiring
// layer.
func (a *App) handleSessionMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a.forwardToTop(m), true
	case tea.QuitMsg:
		a.quitting = true
		return nil, true
	case QuitRequestedMsg:
		// Page-stack tear-down + tea.Quit, in that order. See
		// QuitRequestedMsg's doc for why this can't be a plain
		// tea.Quit return from the quit bindings.
		return a.quitWithCleanup(), true
	case keys.ChordExpiredMsg:
		return a.dispatcher.HandleChordExpired(m), true
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

// handlePollMsg snapshots poll payloads before forwarding to the
// top page. The cache lets a page push that lands later — e.g.
// PushPage from a key handler running in the same Update tick —
// hydrate from the freshest payload via replayCachedDataMsgs
// instead of waiting up to a full poll interval.
//
// DataMsg: the labelled-cache write fires only when the poll
// layer stamped ResourceLabel; legacy DataMsgs from tests skip
// the cache and just forward.
//
// BackendStatusMsg: emitted only on STATE CHANGES, so a backend
// already in the failing state when a page lands would otherwise
// never light up the band on that page. The per-tenant snapshot
// fixes that; empty Detail (recovery transition) prunes the entry
// so a flapping backend that recovered before the push doesn't
// drag a stale error onto the new page.
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
	replayCmd := a.replayCachedDataMsgs()
	return tea.Batch(initCmd, replayCmd)
}

// quitWithCleanup walks the page stack invoking Close() on each
// page so any background work (bulk fanout workers, in-flight
// silence-form Create/UpdateSilence, tenantconfig Status fetch,
// silences editor UpdateSilence) is signalled to cancel before
// bubbletea tears the program down. The returned Cmd batches the
// per-page Close cmds with a terminating tea.Quit so the program
// still exits.
//
// Order is top-of-stack first, mirroring popPage's contract: the
// frontmost page typically holds the most recent in-flight work.
// In practice every Close in this codebase returns nil and just
// fires its stored cancel func synchronously, so the ordering is
// observational; the contract stays explicit so a future Close
// that does emit a Cmd (e.g. an audit-log flush) ships in the
// right order.
//
// Empty stack — pre-PushPage cold-start, or a hypothetical Esc
// loop that popped every page — still emits tea.Quit so a quit
// keystroke during cold start exits cleanly.
//
// CRITICAL: a.quitting is flipped HERE, before the batch returns,
// not in handleLifecycle's `case tea.QuitMsg` branch (which is
// dead code in production — bubbletea's eventLoop catches
// tea.QuitMsg BEFORE dispatching to Update; see vendor
// charm.land/bubbletea/v2 tea.go eventLoop). The cleanup-cascade
// path's terminating tea.Quit emits a QuitMsg into bubbletea's
// message channel; the wiring-layer filter (cmd/tui.go's
// newQuitFilter) reads a.Quitting() to decide pass-through vs.
// rewrite-to-QuitRequestedMsg. Without the flip here, the filter
// would loop QuitMsg back into QuitRequestedMsg forever and the
// program would never exit. The dead case in handleLifecycle stays
// as belt-and-braces.
func (a *App) quitWithCleanup() tea.Cmd {
	a.quitting = true
	cmds := make([]tea.Cmd, 0, len(a.stack)+1)
	for i := len(a.stack) - 1; i >= 0; i-- {
		if c := a.stack[i].Close(); c != nil {
			cmds = append(cmds, c)
		}
	}
	cmds = append(cmds, tea.Quit)
	return tea.Batch(cmds...)
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
	replayCmd := a.replayCachedDataMsgs()
	return tea.Batch(cmd, replayCmd)
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

// cacheStatusMsg stores the latest BackendStatusMsg per tenant.
// Empty Detail signals a recovery transition; pruning the entry
// keeps the cache aligned with the per-page error-band semantics
// (every page's BackendStatusMsg handler does the same delete-on-
// empty-Detail). Subsequent transitions for the same tenant
// overwrite — the cache is a snapshot, not a history.
func (a *App) cacheStatusMsg(m poll.BackendStatusMsg) {
	if m.Detail == "" {
		delete(a.statusCache, m.Tenant)
		return
	}
	a.statusCache[m.Tenant] = m
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
// Cmds returned from each replayed Update are collected and folded
// into a tea.Batch so they survive: every production page returns
// nil from DataMsg today, so the batch is degenerate, but a future
// page wiring DataMsg → kick a follow-up Cmd would otherwise lose
// the kick on the first push.
//
// BackendStatusMsg entries are replayed unconditionally — the error
// band is a page-level UX every list page (alerts / silences /
// groups / receivers) wants to render the same way, and a page
// that doesn't care drops the message in its Update's type switch
// at near-zero cost. Filtering them by PollResources would require
// every list page to also list "status" in its label set, which
// would couple the error-band wiring to an unrelated concept.
//
// The inner-loop write to a.stack[len-1] is safe because the App's
// Update path is single-threaded by bubbletea — replay runs
// inside the same Update call that pushed the page, so no other
// goroutine reads or writes the stack while this loop runs.
func (a *App) replayCachedDataMsgs() tea.Cmd {
	if len(a.stack) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	allowed, filtering := a.replayFilter()
	for label, bucket := range a.pollCache {
		if filtering {
			if _, ok := allowed[label]; !ok {
				continue
			}
		}
		for _, m := range bucket {
			top, cmd := a.stack[len(a.stack)-1].Update(m)
			a.stack[len(a.stack)-1] = top
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	for _, m := range a.statusCache {
		top, cmd := a.stack[len(a.stack)-1].Update(m)
		a.stack[len(a.stack)-1] = top
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
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

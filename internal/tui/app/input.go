// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// toggleTimeFormatCmd flips the app's TimeFormat and emits the
// announcement message + a flash. Pages that don't observe the
// message ignore it (the dispatcher fires regardless of which
// page is on top of the stack).
func (a *App) toggleTimeFormatCmd() tea.Cmd {
	if a.timeFormat == timerender.Relative {
		a.timeFormat = timerender.Absolute
	} else {
		a.timeFormat = timerender.Relative
	}
	captured := a.timeFormat
	return tea.Batch(
		func() tea.Msg { return TimeFormatChangedMsg{Format: captured} },
		func() tea.Msg {
			return footer.FlashShowMsg{
				Level: footer.FlashInfo,
				Text:  "time: " + captured.String(),
			}
		},
	)
}

// registerTenantBindings wires the numeric quick-switch keys
// (`0` for all-tenants, `1`-`9` for the Nth configured backend)
// at LayerGlobal. Pressing one emits a ScopeChangedMsg the top
// page consumes to filter its view and update its title.
func (a *App) registerTenantBindings() {
	a.dispatcher.Set(keys.LayerGlobal, "0", func() tea.Cmd {
		return func() tea.Msg { return ScopeChangedMsg{Scope: "all"} }
	})
	for i, name := range a.tenants {
		if i >= 9 {
			break // numeric quick-switch tops out at 1-9 per C3
		}
		captured := name
		a.dispatcher.Set(keys.LayerGlobal, strconv.Itoa(i+1), func() tea.Cmd {
			return func() tea.Msg { return ScopeChangedMsg{Scope: captured} }
		})
	}
}

// registerGlobalBindings wires the keybindings.md §Global entries
// the app shell owns directly. Tenant quick-switch (#35) ships
// with its own subsystem so it can be unit-tested in isolation.
//
// User-extensible bindings go through SetAction with the stable
// action names documented in `<config-dir>/keys/<profile>.yaml`
// (per ADR 0010); chord prefixes and dispatcher hooks stay on Set
// because the v0.0.1 schema only lets users target named globals.
func (a *App) registerGlobalBindings() {
	a.dispatcher.SetAction(keys.LayerGlobal, "force-quit", "Ctrl+C", quitRequestedCmd)
	a.dispatcher.SetAction(keys.LayerGlobal, "quit", "q", quitRequestedCmd)
	// `t` flips the app-global time-format toggle (Q7.1 — alerts'
	// state-filter cycle moved to Shift+F to free this slot).
	// Emits TimeFormatChangedMsg so every page that renders
	// durations re-renders, and a flash so the user sees the
	// switch took effect (Q7.5).
	a.dispatcher.SetAction(keys.LayerGlobal, "time-format", "t", a.toggleTimeFormatCmd)
	// `Esc` falls through to "pop stack" at the global layer per
	// keybindings.md. Modal / prompt layers shadow this when active
	// so Esc dismisses them first.
	a.dispatcher.SetAction(keys.LayerGlobal, "back", "Esc", PopPage)
	// `:` opens the command bar; `/` opens the filter prompt. The
	// dispatcher only fires the open; the resulting PromptSubmitted
	// / PromptCancelled messages are handled by handleInput later.
	a.dispatcher.SetAction(keys.LayerGlobal, "command", ":", a.openPromptCmd(footer.PromptCommand))
	a.dispatcher.SetAction(keys.LayerGlobal, "filter", "/", a.openPromptCmd(footer.PromptFilter))
	// `Ctrl+T` opens the tenant picker per C3 — fuzzy search over
	// the configured backends with multi-select. Resulting
	// PickerSubmittedMsg is translated into a ScopeChangedMsg in
	// handleLifecycle so every list page reacts the same way as
	// for the numeric quick-switch.
	a.dispatcher.SetAction(keys.LayerGlobal, "tenant-picker", "Ctrl+T", func() tea.Cmd {
		return OpenModal(func() modal.Modal {
			// Tagged "scope" so the lifecycle router knows this
			// submission feeds the global scope; pickers opened by
			// pages (e.g. the silence form's tenant row) carry a
			// different Origin and are forwarded to the page instead.
			return modal.NewPicker("tenants", a.tenants, modal.PickerMulti).
				WithOrigin(PickerOriginScope)
		})
	})
	// `?` opens the k9s-style help overlay. The bindings are
	// composed at open-time so the RESOURCE column always reflects
	// whichever page is on top of the stack. Globals and table
	// motions are curated lists kept here (rather than re-derived
	// from the dispatcher, which stores handlers, not descriptions).
	a.dispatcher.SetAction(keys.LayerGlobal, "help", "?", func() tea.Cmd {
		return OpenModal(func() modal.Modal {
			return help.New(help.Options{
				PageName:     a.activeViewLabel(),
				PageBindings: a.activePageBindings(),
				Globals:      globalsCatalog(),
				TableMotions: tableMotionsCatalog(),
				Tenants:      a.tenants,
				ReadOnly:     a.readOnly,
				Styles:       a.styles,
			})
		})
	})
}

// pickerSelectionsToScope folds the tenant picker's submitted
// selections into the scope string the rest of the app uses.
// Empty input or a selection covering every configured tenant
// both resolve to "all" so the title stays uniform across the
// numeric quick-switch and the picker submit paths. A subset is
// rendered as a stable comma-joined list in `tenants` order so
// `<1>` / `<2>` per-row glyphs on the tenant page render
// predictably.
func pickerSelectionsToScope(selections, tenants []string) string {
	if len(selections) == 0 || len(selections) == len(tenants) {
		return "all"
	}
	picked := make(map[string]struct{}, len(selections))
	for _, s := range selections {
		picked[s] = struct{}{}
	}
	out := make([]string, 0, len(picked))
	for _, t := range tenants {
		if _, ok := picked[t]; ok {
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

// globalsCatalog is the GENERAL-column list rendered in the help
// overlay. Source-of-truth pairs with `keybindings.md §Global` so
// any binding the dispatcher gains a registration for above shows
// up here too.
func globalsCatalog() []action.Action {
	return []action.Action{
		{Key: ":", Description: "command"},
		{Key: "/", Description: "filter"},
		{Key: "?", Description: "help"},
		{Key: "r", Description: "refresh"},
		{Key: "t", Description: "time format"},
		{Key: "Esc", Description: "back"},
		{Key: "q", Description: "quit"},
		{Key: "Ctrl+C", Description: "force quit"},
		{Key: "Ctrl+T", Description: "tenant picker"},
	}
}

// tableMotionsCatalog is the NAVIGATION-column list. Mirrors the
// table-context block from `keybindings.md` so the help overlay
// reads the same affordances the dispatcher serves.
func tableMotionsCatalog() []action.Action {
	return []action.Action{
		{Key: "j", Description: "down"},
		{Key: "k", Description: "up"},
		{Key: "h", Description: "prev column"},
		{Key: "l", Description: "next column"},
		{Key: "gg", Description: "top"},
		{Key: "G", Description: "bottom"},
		{Key: "Ctrl+D", Description: "half page down"},
		{Key: "Ctrl+U", Description: "half page up"},
		{Key: "Ctrl+F", Description: "page down"},
		{Key: "Ctrl+B", Description: "page up"},
		{Key: "Enter", Description: "drill"},
		{Key: "Space", Description: "mark"},
	}
}

// activePageBindings returns the top-of-stack page's Bindings(),
// or nil when no page is pushed (the empty-app placeholder body).
func (a *App) activePageBindings() []action.Action {
	if p := a.topPage(); p != nil {
		return p.Bindings()
	}
	return nil
}

// openPromptCmd returns a Handler that opens the bottom-strip
// prompt in the given mode. State mutation runs synchronously when
// the dispatcher fires; for filter mode, an Opened message
// reaches the top page so it can snapshot pre-filter state per
// PromptOpenedMsg's contract.
//
// The matching history ring is picked at open-time (not at
// constructor time) because `/` on the silences page walks a
// different ring than `/` on the alerts page — the active page is
// only known when the user presses the key.
func (a *App) openPromptCmd(mode footer.PromptMode) func() tea.Cmd {
	return func() tea.Cmd {
		hist := a.histories.historyFor(mode, a.activeViewLabel())
		a.prompt = a.prompt.OpenWithHistory(mode, hist)
		if mode == footer.PromptFilter {
			return func() tea.Msg { return footer.PromptOpenedMsg{Mode: mode} }
		}
		return nil
	}
}

// handleInput covers the input pipeline: prompt results, paste,
// key-release / paste-framing no-ops, and key presses. Returns
// (cmd, true) when handled.
func (a *App) handleInput(msg tea.Msg) (tea.Cmd, bool) {
	switch m := msg.(type) {
	case footer.PromptSubmittedMsg:
		return a.handlePromptSubmitted(m), true
	case footer.PromptCancelledMsg:
		// Filter cancellations flow through to the top page so a
		// page that snapshotted its filter state on prompt-open can
		// roll back. Command cancellations terminate at the App;
		// no observable state change.
		if m.Mode == footer.PromptFilter {
			return a.forwardToTop(m), true
		}
		return nil, true
	case tea.PasteMsg:
		if a.prompt.IsOpen() {
			var cmd tea.Cmd
			a.prompt, cmd = a.prompt.Update(m)
			return cmd, true
		}
		return a.forwardToTop(m), true
	case tea.KeyReleaseMsg, tea.PasteStartMsg, tea.PasteEndMsg:
		// Bubbletea v2 emits release events alongside key presses
		// when key-release reporting is enabled, plus paste-start /
		// paste-end framing around bracketed paste. The app shell
		// has no use for either today; explicit no-ops keep them out
		// of the flash catch-all so the routing intent is auditable.
		return nil, true
	case tea.MouseWheelMsg:
		return a.handleMouseWheel(m), true
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseMotionMsg:
		// Keyboard-first contract: the app enables mouse cell-motion
		// only to receive wheel ticks. Click / release / motion are
		// captured by the terminal in this mode but we explicitly
		// drop them rather than implementing click-to-focus or
		// drag-select. Explicit no-op so they don't fall through to
		// forwardToTop, where a misinterpreting page could attach
		// behaviour we don't want.
		return nil, true
	case tea.KeyPressMsg:
		_, cmd := a.handleKey(m)
		return cmd, true
	}
	return nil, false
}

// handleMouseWheel routes a wheel event. Precedence mirrors
// handleKey: an open modal (the help overlay scrolls; other modals
// ignore the event), then prompt / input-capture (suppress so the
// wheel doesn't grow a phantom motion behind a typing user), then
// the top page (translate up/down ticks into a synthetic 'k'/'j'
// key press so the page's existing vim-motion path runs without
// per-page wheel plumbing). Left/right wheel ticks are ignored —
// pages don't bind h/l to a wheel motion and the horizontal-wheel
// hardware is rare enough that surprising the user with column
// walks is worse than dropping the event.
func (a *App) handleMouseWheel(m tea.MouseWheelMsg) tea.Cmd {
	if a.modal != nil {
		next, cmd := a.modal.Update(m)
		a.modal = next
		return cmd
	}
	if a.prompt.IsOpen() || a.topPageCapturesInput() {
		return nil
	}
	key, ok := wheelToKey(m)
	if !ok {
		return nil
	}
	return a.forwardToTop(key)
}

// wheelToKey maps a vertical wheel tick to the synthetic key press
// each page's vim-motion handler consumes. Horizontal ticks return
// (zero, false) so the caller can drop them. Kept package-private
// so the mapping table lives next to the dispatcher seam that uses
// it; tested via TestApp_MouseWheel*.
func wheelToKey(m tea.MouseWheelMsg) (tea.KeyPressMsg, bool) {
	switch m.Button {
	case tea.MouseWheelUp:
		return tea.KeyPressMsg{Code: 'k', Text: "k"}, true
	case tea.MouseWheelDown:
		return tea.KeyPressMsg{Code: 'j', Text: "j"}, true
	}
	return tea.KeyPressMsg{}, false
}

// handlePromptSubmitted routes a prompt's submission. Command
// values go through cmdbar.Resolve; unknown / ambiguous errors
// surface as Warn flashes; empty input is silent so the user can
// back out of an open `:` prompt by pressing Enter without typing.
// Filter values flow through to the top page, which decides what a
// filter string means in its own context.
func (a *App) handlePromptSubmitted(m footer.PromptSubmittedMsg) tea.Cmd {
	if m.Mode == footer.PromptFilter {
		return a.forwardToTop(m)
	}
	cmd, err := a.cmdbar.Resolve(m.Value)
	if err == nil {
		return cmd
	}
	if errors.Is(err, cmdbar.ErrEmpty) {
		return nil
	}
	return showFlash(footer.FlashWarn, err.Error())
}

// showFlash returns a tea.Cmd that emits a FlashShowMsg with the
// given level and text. Used by the global bindings the app shell
// owns so they can drop a hint without holding a Flash reference.
func showFlash(level footer.FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: level, Text: text}
	}
}

// quitRequestedCmd is the Cmd every quit binding returns instead
// of a bare tea.Quit. The App's handleLifecycle consumes the
// resulting QuitRequestedMsg, Close()s every page on the stack to
// cancel in-flight background work, and emits tea.Quit. See
// QuitRequestedMsg's doc for the bubbletea-runtime detail (QuitMsg
// short-circuits before Update, so the precursor is the only place
// the cleanup can run).
func quitRequestedCmd() tea.Cmd {
	return func() tea.Msg { return QuitRequestedMsg{} }
}

// handleKey routes a single key event. Precedence:
//
//  1. Open modal — captures every key including Esc.
//  2. Open prompt — same rule for the bottom-strip prompt.
//  3. Top page in input-capture mode (forms) — raw keys so the
//     user can type globally-bound chars into fields.
//  4. Dispatcher (modal > prompt > view > table > global).
//  5. Top page — final catch-all for vim motions and per-page
//     shortcuts that don't need pre-registration.
//
// Unconsumed keys drop silently.
func (a *App) handleKey(m tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if a.modal != nil {
		next, cmd := a.modal.Update(m)
		a.modal = next
		return a, cmd
	}
	if a.prompt.IsOpen() {
		var cmd tea.Cmd
		a.prompt, cmd = a.prompt.Update(m)
		return a, cmd
	}
	if a.topPageCapturesInput() {
		// Bypass dispatcher entirely so global bindings (q, :, /,
		// ?, digits) don't shadow text input on the form.
		cmd := a.forwardToTop(m)
		return a, cmd
	}

	keyName := normalizeKey(m)
	if keyName == "" {
		return a, nil
	}
	consumed, cmd := a.dispatcher.Dispatch(keyName)
	if consumed {
		return a, cmd
	}
	// Unbound at the dispatcher: forward to the top page so it can
	// react (vim motions, custom shortcuts) without the app shell
	// pre-knowing every page's binding set.
	cmd = a.forwardToTop(m)
	return a, cmd
}

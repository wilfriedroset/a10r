// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// TestApp_HelpKeyOpensHelpSlot pins the post-ADR-0020 contract: `?`
// installs the help overlay in its own slot (a.overlays.help), not the modal
// slot. The dispatcher's `?` Cmd lands as openHelpMsg; handleLifecycle
// consumes it.
func TestApp_HelpKeyOpensHelpSlot(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	updated, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	a = updated.(*App)
	require.NotNil(t, cmd, "? must produce a Cmd that opens the help overlay")
	drive(t, a, cmd)
	require.NotNil(t, a.overlays.help, "? must populate the help slot")
	require.Nil(t, a.overlays.modal, "? must NOT touch the modal slot")
}

// TestApp_HelpScrollKeyAbsorbedByHelp covers the scroll-vs-dismiss
// split inside the help overlay: vim-style motion keys advance the
// overlay's scroll offset, do not emit a ClosedMsg, and never reach
// the dispatcher (so a `j` over open help does not move the page's
// table cursor underneath).
func TestApp_HelpScrollKeyAbsorbedByHelp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	updated, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	a = updated.(*App)
	drive(t, a, cmd)
	require.NotNil(t, a.overlays.help)
	before := len(*page.updateLog)

	for _, key := range []tea.KeyPressMsg{
		{Code: 'j', Text: "j"},
		{Code: 'k', Text: "k"},
		{Code: 'd', Mod: tea.ModCtrl},
	} {
		_, scrollCmd := a.Update(key)
		require.Nil(t, scrollCmd,
			"scroll key %v must not emit a Cmd while help is open", key)
	}
	require.NotNil(t, a.overlays.help, "scroll keys must NOT close the help overlay")
	require.Len(t, *page.updateLog, before,
		"scroll keys on open help must NOT reach the page underneath")
}

// TestApp_HelpDismissKeyClosesHelp covers the dismiss path: q / Esc
// / ? produce a help.ClosedMsg; processing that message clears the
// help slot.
func TestApp_HelpDismissKeyClosesHelp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "q", key: tea.KeyPressMsg{Code: 'q', Text: "q"}},
		{name: "esc", key: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "question-mark", key: tea.KeyPressMsg{Code: '?', Text: "?"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newTestApp(t)
			updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			a = updated.(*App)

			updated, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
			a = updated.(*App)
			drive(t, a, cmd)
			require.NotNil(t, a.overlays.help, "baseline: ? opens the help slot")

			_, cmd = a.Update(tc.key)
			require.NotNil(t, cmd, "dismiss key %q must emit a Cmd", tc.name)
			msg := cmd()
			_, ok := msg.(help.ClosedMsg)
			require.Truef(t, ok,
				"dismiss key %q must emit help.ClosedMsg, got %T", tc.name, msg)
			drive(t, a, cmd)
			require.Nil(t, a.overlays.help,
				"after help.ClosedMsg the help slot must be cleared")
		})
	}
}

// TestTableMotionsCatalogIsPureMotion pins the k9s NAVIGATION-column
// rule: the column holds cursor movement only. `Enter`/drill and
// `Space`/mark are not motions — drill is a view-specific verb
// (RESOURCE) and mark is a cross-cutting Shared verb (GENERAL); both
// would otherwise duplicate the per-page Bindings() the overlay also
// renders.
func TestTableMotionsCatalogIsPureMotion(t *testing.T) {
	t.Parallel()
	for _, a := range tableMotionsCatalog() {
		require.NotEqualf(t, "Enter", a.Key,
			"Enter/drill belongs in RESOURCE, not the NAVIGATION column")
		require.NotEqualf(t, "Space", a.Key,
			"Space/mark belongs in GENERAL, not the NAVIGATION column")
	}
}

// TestGlobalsCatalogListsFilterSigils pins the discoverability fix:
// the `/` prompt auto-detects fuzzy (`~`) and literal (`\`) modes
// from a leading sigil, but those sigils were advertised only in the
// optional (default-off) tips bar. The GENERAL column must list them
// directly after `/` so the modes are reachable from `?` alone.
func TestGlobalsCatalogListsFilterSigils(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	got := a.globalsCatalog()

	slashAt := -1
	for i, b := range got {
		if b.Key == "/" {
			slashAt = i
			break
		}
	}
	require.GreaterOrEqual(t, slashAt, 0, "baseline: the `/` filter global must be present")
	require.GreaterOrEqual(t, len(got), slashAt+3, "two sigil rows must follow `/`")

	require.Equal(t, "~", got[slashAt+1].Key, "`~` must sit immediately after `/`")
	require.Contains(t, got[slashAt+1].Description, "fuzzy")
	require.Equal(t, "\\", got[slashAt+2].Key, "`\\` must sit immediately after `~`")
	require.Contains(t, got[slashAt+2].Description, "literal")
}

// TestApp_HelpKeyShadowedWhileModalOpen pins the modal > help
// precedence (per ADR 0020): with a modal already open, `?` reaches
// the modal (which ignores it) and does NOT open the help overlay.
// Otherwise a stray `?` over a pending destructive-flow confirm
// would dismiss the decision off-screen.
func TestApp_HelpKeyShadowedWhileModalOpen(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	c := modal.NewConfirm("expire silence?", modal.ConfirmDefaultNo)
	drive(t, a, OpenModal(func() modal.Modal { return c }))
	require.NotNil(t, a.overlays.modal, "baseline: confirm modal is open")

	_, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	require.Nil(t, cmd,
		"? while a modal is open must reach the modal (which ignores it), "+
			"not the dispatcher — the modal returns nil")
	require.Nil(t, a.overlays.help, "? must NOT open the help overlay while a modal is up")
	require.NotNil(t, a.overlays.modal, "the modal must stay open — decisions are sticky")
}

// TestApp_ClosedHelpReleasesDispatcher pins the post-dismiss flow:
// after help.ClosedMsg clears the help slot, the dispatcher fires
// again for subsequent keys (e.g. `q` quits, not "press q to dismiss
// the overlay that's already gone").
func TestApp_ClosedHelpReleasesDispatcher(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	updated, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	a = updated.(*App)
	drive(t, a, cmd)
	require.NotNil(t, a.overlays.help)

	// Dismiss with Esc.
	_, cmd = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	drive(t, a, cmd)
	require.Nil(t, a.overlays.help, "baseline: help slot cleared after dismiss")

	// `q` now reaches the dispatcher and emits QuitRequestedMsg.
	_, cmd = a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd, "q after help close must reach the dispatcher")
	require.IsType(t, QuitRequestedMsg{}, cmd(),
		"dispatcher's q binding must fire once the help overlay is gone")
}

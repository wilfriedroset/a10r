// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// newTestApp builds an App wired to the default skin and a fresh
// dispatcher. Tests that need a different theme construct their own
// Options.
func newTestApp(t *testing.T) *App {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return NewApp(Options{
		Styles:     *styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
	})
}

// stripStyle drops ANSI SGR sequences for substring assertions.
func stripStyle(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestApp_InitNoCmd(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	require.Nil(t, a.Init(), "no startup command at v0.1 — polling lifecycle is #24")
}

func TestApp_PreResizeViewIsEmpty(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	// Before WindowSizeMsg, View must not panic and must return an
	// alt-screen view with empty content.
	v := a.View()
	require.True(t, v.AltScreen)
	require.Empty(t, v.Content)
}

func TestApp_ResizePropagates(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	updated, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a = updated.(*App)
	require.Equal(t, 100, a.width)
	require.Equal(t, 30, a.height)

	v := a.View()
	require.True(t, v.AltScreen)
	require.NotEmpty(t, v.Content)

	visible := stripStyle(v.Content)
	require.Contains(t, visible, "tenants:",
		"header must render after resize")
}

func TestApp_CtrlCQuits(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	// Resize first so View doesn't take the empty short-circuit.
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	_, cmd := a.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "Ctrl+C must produce a Cmd")
	require.IsType(t, tea.QuitMsg{}, cmd(),
		"Ctrl+C's Cmd must emit tea.QuitMsg")
}

func TestApp_QQuits(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

func TestApp_HelpKeyOpensModal(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	updated, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	a = updated.(*App)
	require.NotNil(t, cmd, "? must produce a Cmd that opens the help overlay")
	drive(t, a, cmd)
	require.NotNil(t, a.modal, "? must open the help modal")
}

func TestApp_UnknownKeyIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	// `j` is a vim-motion binding registered by tables (#27), not by
	// the app shell. At v0.1 with no page pushed it must be a silent
	// no-op rather than emit a flash.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	a = updated.(*App)
	require.Nil(t, cmd)
	require.False(t, a.flash.IsActive(),
		"unbound keys must NOT raise a flash — pages may bind them later")
}

func TestApp_TenantKeysEmitScopeChangedMsg(t *testing.T) {
	t.Parallel()

	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     *styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	// `0` selects all tenants.
	_, cmd := a.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	require.NotNil(t, cmd, "0 must produce a Cmd")
	require.Equal(t, ScopeChangedMsg{Scope: "all"}, cmd())

	// `1` and `2` map to the configured tenant names in order.
	_, cmd = a.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	require.NotNil(t, cmd)
	require.Equal(t, ScopeChangedMsg{Scope: "prod"}, cmd())

	_, cmd = a.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	require.NotNil(t, cmd)
	require.Equal(t, ScopeChangedMsg{Scope: "staging"}, cmd())

	// `3` is unbound (only two tenants) — silent no-op.
	_, cmd = a.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	require.Nil(t, cmd, "extra digits beyond configured tenants stay unbound")
}

func TestApp_QuitMsgMarksQuitting(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	updated, cmd := a.Update(tea.QuitMsg{})
	a = updated.(*App)
	require.True(t, a.quitting)
	require.Nil(t, cmd, "QuitMsg's actual termination is bubbletea's job")
}

func TestApp_FlashShowAndClear(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	updated, cmd := a.Update(footer.FlashShowMsg{Level: footer.FlashError, Text: "oops"})
	a = updated.(*App)
	require.True(t, a.flash.IsActive())
	require.NotNil(t, cmd, "Show must schedule the auto-clear tick")

	visible := stripStyle(a.View().Content)
	require.Contains(t, visible, "oops")
}

func TestApp_PasteRoutesToOpenPrompt(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	a.prompt = a.prompt.Open(footer.PromptCommand)
	updated, _ = a.Update(tea.PasteMsg{Content: "alerts"})
	a = updated.(*App)
	require.Equal(t, "alerts", a.prompt.Value())
}

func TestApp_PasteIgnoredWhenPromptClosed(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	require.False(t, a.prompt.IsOpen())
	updated, _ = a.Update(tea.PasteMsg{Content: "junk"})
	a = updated.(*App)
	require.Empty(t, a.prompt.Value(),
		"closed prompt must not absorb pasted content")
}

func TestApp_OpenPromptSwallowsKeysExceptEsc(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	a.prompt = a.prompt.Open(footer.PromptCommand)

	// `q` would normally quit. With the prompt open it must go to the
	// prompt buffer instead.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	a = updated.(*App)
	require.Nil(t, cmd, "key into open prompt must not produce Quit")
	require.Equal(t, "q", a.prompt.Value())
	require.True(t, a.prompt.IsOpen())

	// Esc must still reach the prompt's cancel handler so the user
	// can dismiss the prompt with Esc even though it's "open".
	updated, cmd = a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	require.False(t, a.prompt.IsOpen())
	require.NotNil(t, cmd, "Esc on open prompt must emit cancellation")
	msg := cmd()
	cancel, ok := msg.(footer.PromptCancelledMsg)
	require.True(t, ok)
	require.Equal(t, footer.PromptCommand, cancel.Mode)
}

func TestApp_ChordRoundTrip(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	// Register `gg` on the table-context layer so the chord prefix
	// has a binding to complete to.
	hit := false
	a.dispatcher.Set(keys.LayerTable, "gg", func() tea.Cmd {
		hit = true
		return nil
	})

	// First `g` consumes a chord prefix and returns the timeout
	// scheduling Cmd. The App must surface it so bubbletea can fire
	// the eventual ChordExpiredMsg.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	a = updated.(*App)
	require.NotNil(t, cmd, "chord prefix must produce a scheduling Cmd")
	require.False(t, hit, "single `g` must not yet fire the binding")

	// Second `g` completes the chord — the registered handler runs.
	_, _ = a.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.True(t, hit, "second `g` must complete the chord and run the handler")
}

func TestApp_CtrlCRoutedIntoOpenPrompt(t *testing.T) {
	t.Parallel()
	// Lock the chosen behaviour: an open prompt swallows every key
	// including Ctrl+C, matching keybindings.md's "Esc always reaches
	// the modal/prompt to dismiss it" semantics. The user must Esc
	// out of the prompt before Ctrl+C can quit. This is a deliberate
	// safety property — a stray Ctrl+C while typing should not lose
	// in-progress input.
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	a.prompt = a.prompt.Open(footer.PromptCommand)
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.Nil(t, cmd,
		"Ctrl+C while prompt is open must NOT quit; the prompt swallows it")
}

func TestApp_KeyReleaseAndPasteFramingIgnored(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	// None of these may activate the flash via the catch-all branch.
	for _, msg := range []tea.Msg{
		tea.KeyReleaseMsg{Code: 'a'},
		tea.PasteStartMsg{},
		tea.PasteEndMsg{},
	} {
		updated, cmd := a.Update(msg)
		a = updated.(*App)
		require.Nil(t, cmd)
	}
	require.False(t, a.flash.IsActive(),
		"key-release / paste-framing must not leak into the flash")
}

func TestNormalizeKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{name: "plain letter", key: tea.KeyPressMsg{Code: 's', Text: "s"}, want: "s"},
		{name: "ctrl letter", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, want: "Ctrl+C"},
		{name: "shift letter uppercases", key: tea.KeyPressMsg{Code: 'g', Mod: tea.ModShift}, want: "Shift+G"},
		{name: "ctrl+shift", key: tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl | tea.ModShift}, want: "Ctrl+Shift+S"},
		{name: "esc", key: tea.KeyPressMsg{Code: tea.KeyEscape}, want: "Esc"},
		{name: "enter", key: tea.KeyPressMsg{Code: tea.KeyEnter}, want: "Enter"},
		{name: "backspace", key: tea.KeyPressMsg{Code: tea.KeyBackspace}, want: "Backspace"},
		{name: "tab", key: tea.KeyPressMsg{Code: tea.KeyTab}, want: "Tab"},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}, want: "Up"},
		{name: "pgdown", key: tea.KeyPressMsg{Code: tea.KeyPgDown}, want: "PgDown"},
		{name: "modifier-only event drops", key: tea.KeyPressMsg{Code: 0}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, normalizeKey(tc.key))
		})
	}
}

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
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
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

	visible := testutil.StripStyle(v.Content)
	require.Contains(t, visible, "tenants:",
		"header must render after resize")
}

func TestApp_RefreshRequestedRoutesToHandler(t *testing.T) {
	t.Parallel()

	// The page's `r` binding emits app.RefreshRequestedMsg; the App
	// hands the (resource, scope) tuple to the wiring layer's
	// refresh func. Nil-handler runs are covered separately so the
	// no-config / no-poller paths don't crash.
	type call struct{ resource, scope string }
	var got []call
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     *styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		Refresh: func(resource, scope string) {
			got = append(got, call{resource, scope})
		},
	})

	_, cmd := a.Update(RefreshRequestedMsg{Resource: "silences", Scope: "prod"})
	require.Nil(t, cmd, "refresh routing is a side effect; no Cmd")
	require.Equal(t, []call{{"silences", "prod"}}, got,
		"refresh handler must receive the (resource, scope) tuple verbatim")
}

func TestApp_RefreshRequestedNilHandlerIsSafe(t *testing.T) {
	t.Parallel()
	// Without a Refresh func wired (headless tests / no-poller
	// wizard runs) the App must silently swallow the message —
	// pressing `r` early in a wizard run shouldn't crash.
	a := newTestApp(t)
	_, cmd := a.Update(RefreshRequestedMsg{Resource: "silences", Scope: "all"})
	require.Nil(t, cmd)
}

func TestApp_TKeyTogglesTimeFormat(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	require.Equal(t, TimeFormatRelative, a.timeFormat,
		"app starts in relative mode")

	_, cmd := a.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Equal(t, TimeFormatAbsolute, a.timeFormat,
		"first `t` press flips to absolute")
	require.NotNil(t, cmd)

	// Walk the batch and assert both the announce + flash are
	// produced. Order doesn't matter — we only care that both
	// land on the bus.
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok, "Cmd must produce a BatchMsg")
	var sawAnnounce, sawFlash bool
	for _, c := range batch {
		switch m := c().(type) {
		case TimeFormatChangedMsg:
			require.Equal(t, TimeFormatAbsolute, m.Format)
			sawAnnounce = true
		case footer.FlashShowMsg:
			require.Contains(t, m.Text, "absolute")
			sawFlash = true
		}
	}
	require.True(t, sawAnnounce, "must announce the new format")
	require.True(t, sawFlash, "must flash so the user sees the toggle took effect")

	// Second press flips back.
	_, _ = a.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Equal(t, TimeFormatRelative, a.timeFormat)
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

func TestApp_CtrlTOpensTenantPicker(t *testing.T) {
	t.Parallel()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     *styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging", "dev"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a = updated.(*App)

	updated, cmd := a.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	a = updated.(*App)
	require.NotNil(t, cmd, "Ctrl+T must produce a Cmd that opens the picker")
	drive(t, a, cmd)
	require.NotNil(t, a.modal, "Ctrl+T must open a modal")
	require.Equal(t, "tenants", a.modal.Title())
}

func TestPickerSelectionsToScope(t *testing.T) {
	t.Parallel()
	tenants := []string{"prod", "staging", "dev"}
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty falls back to all", nil, "all"},
		{"every tenant folds to all", []string{"prod", "staging", "dev"}, "all"},
		{"single name passes through", []string{"staging"}, "staging"},
		{"subset joined in tenant order", []string{"dev", "prod"}, "prod,dev"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, pickerSelectionsToScope(tc.in, tenants))
		})
	}
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

// TestApp_InputCapturePageBypassesGlobalBindings asserts the
// fix for a user-reported bug: a form-style page that captures
// input must receive globally-bound keys (q / : / / / ? /
// digits) instead of having the dispatcher consume them.
func TestApp_InputCapturePageBypassesGlobalBindings(t *testing.T) {
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

	form := newFakePage("form")
	form.capturesInput = true
	drive(t, a, PushPage(func() Page { return form }))

	// `q` would normally fire tea.Quit at LayerGlobal. With a
	// capturing form on top, the form must receive it raw.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	a = updated.(*App)
	require.False(t, a.quitting,
		"capturing page must shadow the global `q` quit binding")
	require.Nil(t, cmd, "no Cmd is emitted by the fake page on `q`")
	require.Len(t, *form.updateLog, 1, "form must receive the keystroke")
	require.Equal(t, tea.KeyPressMsg{Code: 'q', Text: "q"}, (*form.updateLog)[0])

	// Same for `0` (tenant quick-switch) and `1` and `2`.
	for _, key := range []rune{'0', '1', '2'} {
		_, cmd := a.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		require.Nil(t, cmd,
			"capturing page must shadow the global tenant `%c` binding", key)
	}
	require.Len(t, *form.updateLog, 4, "every digit must reach the form")

	// And `:` / `/` / `?` (prompt and help). None should open a
	// prompt or modal; all must reach the form.
	for _, key := range []rune{':', '/', '?'} {
		_, _ = a.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
	}
	require.False(t, a.prompt.IsOpen(),
		"capturing page must shadow `:` / `/` so they don't open the prompt")
	require.Nil(t, a.modal,
		"capturing page must shadow `?` so it doesn't open the help modal")
	require.Len(t, *form.updateLog, 7, "every globally-bound key must reach the form")
}

// TestApp_NonCapturingPageStillHonoursGlobals is the dual of the
// previous test: a regular page (CapturesInput=false or interface
// not implemented) must still see globals consumed by the
// dispatcher.
func TestApp_NonCapturingPageStillHonoursGlobals(t *testing.T) {
	t.Parallel()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     *styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod"},
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts") // capturesInput defaults to false
	drive(t, a, PushPage(func() Page { return page }))

	// `q` quits. The dispatcher returns the tea.Quit Cmd; drive
	// runs it so the QuitMsg lands at handleLifecycle.
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd, "dispatcher must emit the tea.Quit Cmd for non-capturing pages")
	drive(t, a, cmd)
	require.True(t, a.quitting, "non-capturing page must let `q` reach LayerGlobal")
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

	visible := testutil.StripStyle(a.View().Content)
	require.Contains(t, visible, "oops")
}

func TestApp_PromptPanelRendersAboveBody(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a = updated.(*App)
	// Push a page so the body title is page-driven and the live
	// filter appendage has a real `<resource>(<scope>)[<count>]`
	// to attach to.
	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	// Closed prompt — no extra bordered box appears between the
	// top panel and the body.
	out := testutil.StripStyle(a.View().Content)
	closedFrames := strings.Count(out, "┌")
	require.Equal(t, 1, closedFrames,
		"closed prompt: only the body's bordered frame is visible")

	// Filter prompt open + a typed value. The frame count grows by
	// one (the prompt panel), and the body title carries the live
	// `</value>` segment so the user sees the active filter.
	a.prompt = a.prompt.Open(footer.PromptFilter)
	a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})

	out = testutil.StripStyle(a.View().Content)
	require.Equal(t, 2, strings.Count(out, "┌"),
		"open prompt adds its own bordered panel above the body")
	require.Contains(t, out, "🐩>",
		"filter mode renders the poodle emoji prefix per the k9s mirror")
	require.Contains(t, out, "hi")
	require.Contains(t, out, "</hi>",
		"the body title carries the live filter value while typed")

	// Command mode uses the dog emoji and does NOT touch the title.
	a.prompt = a.prompt.Open(footer.PromptCommand)
	for _, r := range "sil" {
		a.prompt, _ = a.prompt.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	out = testutil.StripStyle(a.View().Content)
	require.Contains(t, out, "🐶>",
		"command mode renders the dog emoji prefix")
	require.NotContains(t, out, "</sil>",
		"command-mode prompt must not bleed into the body title")
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
	// prompt buffer instead. The prompt now also emits a
	// PromptChangedMsg per keystroke so live-filter pages can react;
	// the Cmd must NOT carry tea.QuitMsg.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	a = updated.(*App)
	require.Equal(t, "q", a.prompt.Value())
	require.True(t, a.prompt.IsOpen())
	require.NotNil(t, cmd, "buffer mutation must broadcast PromptChangedMsg")
	_, isQuit := cmd().(tea.QuitMsg)
	require.False(t, isQuit, "key into open prompt must not produce Quit")
	_, isChanged := cmd().(footer.PromptChangedMsg)
	require.True(t, isChanged, "prompt keystroke must produce PromptChangedMsg, not Quit")

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
		{name: "ctrl+backslash", key: tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}, want: "Ctrl+\\"},
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

// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"
	"time"

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
		Styles:     styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
	})
}

func TestApp_InitNoCmd(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	require.Nil(t, a.Init(),
		"no startup command at v0.1 — polling lifecycle is #24, "+
			"and the hint bar is OFF by default so its tick doesn't fire either")
}

func TestApp_InitSchedulesHintBarTickWhenEnabled(t *testing.T) {
	t.Parallel()

	// When the user opts in via `tui.tips: true`, the App's Init
	// must schedule the rotating hint bar's first tick. This is
	// the wiring contract: a disabled bar (the default) returns
	// nil — exercised by TestApp_InitNoCmd above; an enabled bar
	// returns the tea.Tick command.
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		HintBar: footer.NewHintBar(footer.HintBarOptions{
			Enabled:  true,
			Interval: 50 * time.Millisecond,
		}),
	})
	require.NotNil(t, a.Init(),
		"enabled hint bar must schedule the first rotation tick from Init")
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
		Styles:     styles,
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
		Styles:     styles,
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
		Styles:     styles,
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
		Styles:     styles,
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
		Styles:     styles,
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

// TestApp_ViewEnablesMouseCellMotion pins the mouse-mode contract:
// every rendered view (pre- and post-resize) must request cell-
// motion so the terminal forwards wheel ticks. Anything stricter
// (all-motion) would also forward hover events the app drops; cell-
// motion is the minimum that delivers wheel + click for the
// keyboard-first contract enforced by handleInput.
func TestApp_ViewEnablesMouseCellMotion(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	pre := a.View()
	require.Equal(t, tea.MouseModeCellMotion, pre.MouseMode,
		"pre-resize view must already request mouse cell-motion")

	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)
	post := a.View()
	require.Equal(t, tea.MouseModeCellMotion, post.MouseMode,
		"post-resize view keeps mouse cell-motion on")
}

// TestApp_MouseWheelOnTableSynthesizesMotionKey covers the table-
// page case: wheel up / down become a synthetic 'k' / 'j' key
// press routed to the top page, so each page's existing
// cursor.HandleMotion path runs without per-page wheel plumbing.
func TestApp_MouseWheelOnTableSynthesizesMotionKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		wheel  tea.MouseWheelMsg
		expect tea.KeyPressMsg
	}{
		{
			name:   "wheel down -> j",
			wheel:  tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			expect: tea.KeyPressMsg{Code: 'j', Text: "j"},
		},
		{
			name:   "wheel up -> k",
			wheel:  tea.MouseWheelMsg{Button: tea.MouseWheelUp},
			expect: tea.KeyPressMsg{Code: 'k', Text: "k"},
		},
	}
	// Each case spins up its own App so subtests can run in
	// parallel without sharing the page's update log.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := newTestApp(t)
			updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			a = updated.(*App)

			page := newFakePage("alerts")
			drive(t, a, PushPage(func() Page { return page }))
			require.Empty(t, *page.updateLog,
				"baseline: no synthetic key delivered yet")

			_, cmd := a.Update(tc.wheel)
			require.Nil(t, cmd, "fakePage returns nil from Update; no Cmd surfaces")
			require.Len(t, *page.updateLog, 1,
				"wheel must deliver exactly one synthetic key to the top page")
			require.Equal(t, tc.expect, (*page.updateLog)[0])
		})
	}
}

// TestApp_MouseClickAndMotionIgnored guards the keyboard-first
// contract: cell-motion mode also forwards click / release /
// motion events but the app drops them rather than ever attaching
// click-to-focus or drag semantics. Forwarding to the page would
// risk a future page consuming them by accident.
func TestApp_MouseClickAndMotionIgnored(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))
	before := len(*page.updateLog)

	msgs := []tea.Msg{
		tea.MouseClickMsg{Button: tea.MouseLeft, X: 5, Y: 5},
		tea.MouseReleaseMsg{Button: tea.MouseLeft, X: 5, Y: 5},
		tea.MouseMotionMsg{X: 6, Y: 6},
	}
	for _, m := range msgs {
		_, cmd := a.Update(m)
		require.Nil(t, cmd, "click / release / motion must never produce a Cmd")
	}
	require.Len(t, *page.updateLog, before,
		"click / release / motion must NOT reach the top page — keyboard-first")
}

// TestApp_MouseWheelRoutedToHelpModal pins the modal precedence:
// when the help overlay is open a wheel tick goes to the modal
// (so it can scroll its content) and does NOT synthesize a 'j'/'k'
// for the page underneath.
func TestApp_MouseWheelRoutedToHelpModal(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	// Open the help modal.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	a = updated.(*App)
	drive(t, a, cmd)
	require.NotNil(t, a.modal, "help modal must be open before the wheel tick")
	before := len(*page.updateLog)

	_, cmd = a.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Nil(t, cmd, "help modal returns nil on wheel — no Cmd")
	require.NotNil(t, a.modal, "wheel must NOT close the help modal")
	require.Len(t, *page.updateLog, before,
		"wheel on an open modal must NOT reach the page beneath it")
}

// TestApp_MouseWheelSuppressedWhenPromptOpen pins the prompt-mode
// contract: the user is typing a `:` command or `/` filter — a
// stray wheel tick must not move the cursor on the body the user
// can't see anyway (the prompt has all the focus). Same rule for
// input-capture pages (forms).
func TestApp_MouseWheelSuppressedWhenPromptOpen(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	// Open the filter prompt.
	a.prompt = a.prompt.Open(footer.PromptFilter)
	require.True(t, a.prompt.IsOpen())
	before := len(*page.updateLog)

	_, cmd := a.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Nil(t, cmd)
	require.Len(t, *page.updateLog, before,
		"open prompt must shadow the wheel — body cursor stays put")
}

// TestApp_MouseWheelSuppressedOnInputCapturePage covers the form-
// page suppression: a page that opts in to InputCapturePage owns
// every keystroke, so a wheel tick must not leak a synthetic 'j'/
// 'k' into a form whose body the user is typing into.
func TestApp_MouseWheelSuppressedOnInputCapturePage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	form := newFakePage("form")
	form.capturesInput = true
	drive(t, a, PushPage(func() Page { return form }))
	before := len(*form.updateLog)

	_, cmd := a.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Nil(t, cmd)
	require.Len(t, *form.updateLog, before,
		"input-capture page must shadow the wheel — typing focus stays clean")
}

// TestWheelToKey is a small table-driven map check so the wheel-
// button -> synthetic-key mapping is auditable in one place. The
// (false) cases are the explicit "drop this" path.
func TestWheelToKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   tea.MouseWheelMsg
		want tea.KeyPressMsg
		ok   bool
	}{
		{"down -> j", tea.MouseWheelMsg{Button: tea.MouseWheelDown}, tea.KeyPressMsg{Code: 'j', Text: "j"}, true},
		{"up -> k", tea.MouseWheelMsg{Button: tea.MouseWheelUp}, tea.KeyPressMsg{Code: 'k', Text: "k"}, true},
		{"left dropped", tea.MouseWheelMsg{Button: tea.MouseWheelLeft}, tea.KeyPressMsg{}, false},
		{"right dropped", tea.MouseWheelMsg{Button: tea.MouseWheelRight}, tea.KeyPressMsg{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := wheelToKey(tc.in)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
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

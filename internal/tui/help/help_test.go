// SPDX-License-Identifier: Apache-2.0

package help

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func sampleOpts(t *testing.T) Options {
	t.Helper()
	return Options{
		PageName: "alerts",
		PageBindings: []action.Action{
			{Key: "Enter", Description: "detail"},
			{Key: "Space", Description: "mark"},
			{Key: "s", Description: "silence", Dangerous: true},
			{Key: "/", Description: "filter"},
			{Key: "Shift+S", Description: "sort severity"},
			{Key: "Shift+N", Description: "sort name"},
		},
		Globals: []action.Action{
			{Key: ":", DisplayKey: ":cmd", Description: "Command mode"},
			{Key: "/", Description: "filter"},
			{Key: "?", Description: "help"},
			{Key: "Esc", Description: "back"},
			{Key: "q", Description: "quit"},
		},
		TableMotions: []action.Action{
			{Key: "j", Description: "down"},
			{Key: "k", Description: "up"},
			{Key: "gg", Description: "top"},
			{Key: "G", Description: "bottom"},
		},
		Tenants: []string{"primary", "secondary"},
		Styles:  testutil.LoadStyles(t),
	}
}

func TestHelp_ColumnsRender(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	out := testutil.StripStyle(h.View(160, 30))
	for _, col := range []string{"RESOURCE", "GENERAL", "NAVIGATION", "HOTKEYS"} {
		require.Containsf(t, out, col, "column heading %q must appear", col)
	}
}

func TestHelp_ResourceColumnListsTenantsAndPageVerbs(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	out := testutil.StripStyle(h.View(160, 30))

	// Numeric quick-switch comes from the global App layer; the
	// help renders it inside RESOURCE because it changes the
	// active scope of the resource the user is looking at.
	require.Contains(t, out, "<0>")
	require.Contains(t, out, "all")
	require.Contains(t, out, "<1>")
	require.Contains(t, out, "primary")
	require.Contains(t, out, "<2>")
	require.Contains(t, out, "secondary")

	// Page-specific verbs follow.
	require.Contains(t, out, "<enter>")
	require.Contains(t, out, "detail")
	require.Contains(t, out, "<space>")
	require.Contains(t, out, "mark")
}

func TestHelp_ReadOnlyHidesDangerous(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.ReadOnly = true
	h := New(opts)
	out := testutil.StripStyle(h.View(160, 30))

	require.NotContains(t, out, "silence",
		"`s silence` is Dangerous and must be hidden in read-only mode")
	require.Contains(t, out, "filter",
		"non-Dangerous bindings stay visible")
}

func TestHelp_StaticColumnsRenderCuratedEntries(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	out := testutil.StripStyle(h.View(160, 30))

	for _, want := range []string{"<:cmd>", "Command mode", "<?>", "help", "<esc>", "back"} {
		require.Containsf(t, out, want, "GENERAL column must surface %q", want)
	}
	require.NotContains(t, out, "<:>",
		"the bare `<:>` chip must be replaced by `<:cmd>` everywhere (ADR 0038)")
	for _, want := range []string{"<j>", "down", "<gg>", "top", "<shift-g>", "bottom"} {
		require.Containsf(t, out, want, "NAVIGATION column must surface %q", want)
	}
	for _, want := range []string{"<shift-s>", "sort severity"} {
		require.Containsf(t, out, want, "HOTKEYS column must surface %q", want)
	}
}

// TestHelp_DismissKeysEmitClosed pins the dismiss contract: q, Esc,
// and ? still close the overlay (the latter is the same key that
// opened it — pressing it again toggles off).
func TestHelp_DismissKeysEmitClosed(t *testing.T) {
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
			h := New(sampleOpts(t))
			_, cmd := h.Update(tc.key)
			require.NotNil(t, cmd, "dismiss key %q must emit a command", tc.name)
			msg := cmd()
			_, ok := msg.(ClosedMsg)
			require.Truef(t, ok, "dismiss key %q must emit help.ClosedMsg", tc.name)
		})
	}
}

// TestHelp_ScrollKeysDoNotDismiss covers the bug fix: vim-style
// scroll keys (j/k/g/G/Ctrl+D/Ctrl+U/Ctrl+F/Ctrl+B plus the arrow
// and page navigation keys) must adjust the scroll offset instead
// of closing the overlay. Wheel-only scroll is undiscoverable, so
// users reflexively press j/k to scroll a long help body — that
// path must keep the overlay open.
func TestHelp_ScrollKeysDoNotDismiss(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "j", key: tea.KeyPressMsg{Code: 'j', Text: "j"}},
		{name: "k", key: tea.KeyPressMsg{Code: 'k', Text: "k"}},
		{name: "g", key: tea.KeyPressMsg{Code: 'g', Text: "g"}},
		{name: "G", key: tea.KeyPressMsg{Code: 'G', Text: "G"}},
		{name: "down", key: tea.KeyPressMsg{Code: tea.KeyDown}},
		{name: "up", key: tea.KeyPressMsg{Code: tea.KeyUp}},
		{name: "pgdown", key: tea.KeyPressMsg{Code: tea.KeyPgDown}},
		{name: "pgup", key: tea.KeyPressMsg{Code: tea.KeyPgUp}},
		{name: "home", key: tea.KeyPressMsg{Code: tea.KeyHome}},
		{name: "end", key: tea.KeyPressMsg{Code: tea.KeyEnd}},
		{name: "space", key: tea.KeyPressMsg{Code: tea.KeySpace}},
		{name: "ctrl+d", key: tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}},
		{name: "ctrl+u", key: tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}},
		{name: "ctrl+f", key: tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}},
		{name: "ctrl+b", key: tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := New(sampleOpts(t))
			next, cmd := h.Update(tc.key)
			require.Nilf(t, cmd, "scroll key %q must NOT emit a help.ClosedMsg", tc.name)
			require.Samef(t, h, next,
				"scroll key %q returns the same overlay — no transition", tc.name)
		})
	}
}

// TestHelp_JScrollsDown verifies j advances the scroll offset on
// an overflowing help payload (mirrors the wheel-down test). With
// view height 4 and nine tenants forcing overflow, a j press must
// shift the visible rows.
func TestHelp_JScrollsDown(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	h := New(opts)

	const w, hgt = 160, 4
	first := testutil.StripStyle(h.View(w, hgt))

	_, cmd := h.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Nil(t, cmd, "j must not dismiss the overlay")

	scrolled := testutil.StripStyle(h.View(w, hgt))
	require.NotEqual(t, first, scrolled,
		"j on overflowing content must shift the visible rows")
}

// TestHelp_KAtTopIsNoOp verifies k clamps at scroll=0 (mirrors the
// wheel-up upper-bound test).
func TestHelp_KAtTopIsNoOp(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	h := New(opts)

	const w, hgt = 160, 4
	first := testutil.StripStyle(h.View(w, hgt))

	_, cmd := h.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Nil(t, cmd, "k must not dismiss the overlay")

	require.Equal(t, first, testutil.StripStyle(h.View(w, hgt)),
		"k at scroll=0 must clamp — visible rows unchanged")
}

// TestHelp_GJumpsToBottomGgJumpsToTop verifies G scrolls to the
// last reachable row and g scrolls back to the top.
func TestHelp_GJumpsToBottomGgJumpsToTop(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	h := New(opts)

	const w, hgt = 160, 4
	top := testutil.StripStyle(h.View(w, hgt))

	_, cmd := h.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Nil(t, cmd)
	bottom := testutil.StripStyle(h.View(w, hgt))
	require.NotEqual(t, top, bottom, "G must scroll to the bottom")

	_, cmd = h.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Nil(t, cmd)
	backToTop := testutil.StripStyle(h.View(w, hgt))
	require.Equal(t, top, backToTop, "g must scroll back to the top")
}

func TestHelp_NonKeyMessageIsIgnored(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	type custom struct{}
	_, cmd := h.Update(custom{})
	require.Nil(t, cmd)
}

func TestHelp_NumericListClampsAtNine(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	// Twelve configured backends — the catalog only goes to 9
	// (the digit budget of the keyboard's number row).
	opts.Tenants = []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l",
	}
	h := New(opts)
	out := testutil.StripStyle(h.View(200, 40))

	require.Contains(t, out, "<9>")
	require.NotContains(t, out, "<10>",
		"numeric quick-switch tops out at <9>; extras are reachable via Ctrl+T")
}

// TestHelp_WheelDownScrollsContent covers the happy-path mouse-
// wheel flow on a help payload that overflows the modal height:
// a wheel-down tick scrolls the rendered rows down by one so a
// hidden bottom row becomes visible without the modal closing.
func TestHelp_WheelDownScrollsContent(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	// Force overflow: nine tenants fill the RESOURCE column down to
	// `<9>` (plus `<0> all` on top + the four page verbs) — well past
	// the small modal height we'll render at.
	opts.Tenants = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	h := New(opts)

	const w, hgt = 160, 4 // height = 4 forces a multi-row scroll
	first := testutil.StripStyle(h.View(w, hgt))

	next, cmd := h.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Nil(t, cmd, "wheel must not emit a help.ClosedMsg")
	require.Same(t, h, next, "wheel returns the same overlay — no transition")

	scrolled := testutil.StripStyle(h.View(w, hgt))
	require.NotEqual(t, first, scrolled,
		"wheel-down on overflowing content must shift the visible rows")
}

// TestHelp_WheelUpClampsAtTop covers the upper-bound edge case:
// a wheel-up tick at the top is a no-op (scroll never goes
// negative) so the rendered rows stay the same as the initial view.
func TestHelp_WheelUpClampsAtTop(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	h := New(opts)

	const w, hgt = 160, 4
	first := testutil.StripStyle(h.View(w, hgt))

	_, cmd := h.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	require.Nil(t, cmd)

	again := testutil.StripStyle(h.View(w, hgt))
	require.Equal(t, first, again,
		"wheel-up at scroll=0 must clamp — visible rows unchanged")
}

// TestHelp_WheelDownClampsAtBottom covers the lower-bound edge
// case: many wheel-down ticks past the last row must still leave
// at least one visible row (the View clamps scroll to maxScroll).
func TestHelp_WheelDownClampsAtBottom(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	h := New(opts)

	const w, hgt = 160, 4
	for range 100 {
		_, _ = h.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}

	out := testutil.StripStyle(h.View(w, hgt))
	require.NotEmpty(t, out,
		"runaway wheel-down must still leave visible content — scroll clamps")
	// Sanity: one more wheel-up scrolls back, proving scroll is not
	// stranded past the maximum.
	_, _ = h.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	require.NotEqual(t, out, testutil.StripStyle(h.View(w, hgt)),
		"after clamp, wheel-up still produces a visible change")
}

// TestHelp_WheelDoesNotEmitHelpClosed pins the overlay-close contract:
// every keystroke dismisses the help, but wheel ticks must NOT —
// otherwise the user can never scroll, the first wheel event would
// close the overlay.
func TestHelp_WheelDoesNotEmitHelpClosed(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	for _, button := range []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown} {
		_, cmd := h.Update(tea.MouseWheelMsg{Button: button})
		require.Nil(t, cmd, "wheel must NEVER emit help.ClosedMsg (button=%v)", button)
	}
}

func TestHelp_NoTenantsDropsNumericBlock(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = nil
	h := New(opts)
	out := testutil.StripStyle(h.View(160, 30))

	require.NotContains(t, out, "<0>",
		"empty tenant list drops the numeric block entirely — "+
			"otherwise `<0> all` reads as a no-op key")
	require.Contains(t, out, "RESOURCE",
		"the column heading still renders so the page verbs have a banner")
}

func TestChipText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, key, want string
	}{
		{"lowercase letter", "s", "<s>"},
		{"bare uppercase letter expands to shift", "S", "<shift-s>"},
		{"shift chord", "Shift+F", "<shift-f>"},
		{"ctrl chord", "Ctrl+E", "<ctrl-e>"},
		{"word key", "Enter", "<enter>"},
		{"slash symbol", "/", "</>"},
		{"question mark", "?", "<?>"},
		{"digit", "0", "<0>"},
		{"vim chord stays lowercase", "gg", "<gg>"},
		{"ligature-prone dash keeps square brackets", "-", "[-]"},
		{"ligature-prone less-than keeps square brackets", "<", "[<]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ChipText(tc.key))
		})
	}
}

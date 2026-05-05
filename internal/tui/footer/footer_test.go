// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

// ----- crumbs -----

func TestCrumbs_PushPopRender(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	c := NewCrumbs()

	require.Empty(t, c.Render(styles), "empty crumbs render as empty")
	require.Equal(t, 0, c.Len())

	c = c.Push("alerts")
	c = c.Push("detail")
	require.Equal(t, 2, c.Len())
	require.Equal(t, "detail", c.Top())

	out := testutil.StripStyle(c.Render(styles))
	require.Contains(t, out, "<alerts>")
	require.Contains(t, out, "<detail>")
	require.Contains(t, out, crumbSeparator)

	c = c.Pop()
	require.Equal(t, 1, c.Len())
	require.Equal(t, "alerts", c.Top())

	out = testutil.StripStyle(c.Render(styles))
	require.Contains(t, out, "<alerts>")
	require.NotContains(t, out, "<detail>")
	require.NotContains(t, out, crumbSeparator,
		"single-entry crumb has no separator")
}

func TestCrumbs_PopOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()

	c := NewCrumbs().Pop()
	require.Equal(t, 0, c.Len(),
		"over-popping must be a no-op so a stray Esc at the root never panics")
}

func TestCrumbs_Set(t *testing.T) {
	t.Parallel()

	c := NewCrumbs().Set([]string{"alerts", "detail", "silence"})
	require.Equal(t, 3, c.Len())
	require.Equal(t, "silence", c.Top())
}

func TestCrumbs_SetIsDefensiveCopy(t *testing.T) {
	t.Parallel()

	src := []string{"alerts", "detail"}
	c := NewCrumbs().Set(src)
	src[0] = "mutated"
	require.NotEqual(t, "mutated", c.Render(loadStyles(t)),
		"Set must copy the input slice so external mutation doesn't bleed in")
}

// ----- prompt -----

func TestPrompt_OpenAcceptsKeystrokes(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	require.True(t, p.IsOpen())
	require.Equal(t, PromptCommand, p.Mode())
	require.Empty(t, p.Value())

	// Type "alerts"
	for _, r := range "alerts" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "alerts", p.Value())
}

func TestPrompt_BackspaceRemovesLastRune(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	for _, r := range "alerts" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "alert", p.Value())
}

func TestPrompt_BackspaceMultiByteRune(t *testing.T) {
	t.Parallel()

	// Regression: a byte-slicing implementation would corrupt this.
	p := NewPrompt().Open(PromptFilter)
	for _, r := range "caféñ" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "caféñ", p.Value())

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "café", p.Value(),
		"backspace must pop one rune, not one byte (ñ is 2 bytes in UTF-8)")
}

func TestPrompt_BackspaceOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.True(t, p.IsOpen(), "backspace on empty must NOT close the prompt")
	require.Empty(t, p.Value())
	require.Nil(t, cmd)
}

func TestPrompt_NonPrintableKeysIgnored(t *testing.T) {
	t.Parallel()

	// Arrow keys, F-keys, and modifier-only events leave Text empty
	// and have non-printable Codes — they must not corrupt the buffer.
	p := NewPrompt().Open(PromptCommand)
	for _, code := range []rune{tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd} {
		p, _ = p.Update(tea.KeyPressMsg{Code: code})
	}
	require.Empty(t, p.Value(),
		"non-printable navigation keys must not be appended")
}

func TestPrompt_PasteAppendsContent(t *testing.T) {
	t.Parallel()

	// Bracketed paste should append the pasted content as a single
	// chunk so users can paste a UID / labelset into the command bar.
	p := NewPrompt().Open(PromptCommand)
	p, cmd := p.Update(tea.PasteMsg{Content: "alertname=High_CPU"})
	require.Equal(t, "alertname=High_CPU", p.Value())
	require.NotNil(t, cmd, "paste mutates the buffer; live-filter pages need a Changed broadcast")
	require.Equal(t,
		PromptChangedMsg{Mode: PromptCommand, Value: "alertname=High_CPU"},
		cmd(),
		"paste must emit a PromptChangedMsg with the post-paste value")
}

func TestPrompt_CodeFallbackForEmptyText(t *testing.T) {
	t.Parallel()

	// Some terminals report a printable rune via Code without
	// populating Text. The prompt must still accept it.
	p := NewPrompt().Open(PromptFilter)
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a'})
	require.Equal(t, "a", p.Value())
}

func TestPrompt_CtrlUClearsBuffer(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptFilter)
	for _, r := range "high cpu" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "high cpu", p.Value())

	p, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, p.Value(), "Ctrl+U clears the buffer per keybindings.md")
}

func TestPrompt_EnterEmitsSubmittedAndCloses(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	for _, r := range "sil" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, p.IsOpen(), "submit must close the prompt")
	require.Empty(t, p.Value(), "submit must clear the buffer")
	require.NotNil(t, cmd)

	msg := cmd()
	submitted, ok := msg.(PromptSubmittedMsg)
	require.True(t, ok)
	require.Equal(t, PromptCommand, submitted.Mode)
	require.Equal(t, "sil", submitted.Value)
}

func TestPrompt_EscEmitsCancelledAndCloses(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptFilter)
	for _, r := range "stuff" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, p.IsOpen())
	require.NotNil(t, cmd)
	cancel, ok := cmd().(PromptCancelledMsg)
	require.True(t, ok)
	require.Equal(t, PromptFilter, cancel.Mode)
}

func TestPrompt_ClosedIgnoresKeys(t *testing.T) {
	t.Parallel()

	p := NewPrompt()
	require.False(t, p.IsOpen())
	p, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.Nil(t, cmd)
	require.Empty(t, p.Value(),
		"a closed prompt must not accept keystrokes")
}

func TestPrompt_BackspaceEmitsChanged(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptFilter)
	for _, r := range "hi" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.NotNil(t, cmd, "backspace mutates the buffer; live-filter pages need a Changed broadcast")
	require.Equal(t,
		PromptChangedMsg{Mode: PromptFilter, Value: "h"},
		cmd(),
		"backspace must emit PromptChangedMsg with the post-edit buffer")
	require.Equal(t, "h", p.Value())
}

func TestPrompt_BackspaceOnEmptyDoesNotEmitChanged(t *testing.T) {
	t.Parallel()

	// No-op edits don't emit Changed — pages would otherwise see
	// spurious recomputes for keystrokes that didn't move the
	// buffer.
	p := NewPrompt().Open(PromptFilter)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd, "no-op backspace must not broadcast Changed")
}

func TestPrompt_CtrlUEmitsChangedOnce(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptFilter)
	for _, r := range "junk" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p, cmd := p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "Ctrl+U clears the buffer; live-filter pages need a Changed broadcast")
	require.Equal(t,
		PromptChangedMsg{Mode: PromptFilter, Value: ""},
		cmd(),
		"Ctrl+U must emit PromptChangedMsg with an empty value")
	require.Empty(t, p.Value())

	// Ctrl+U on an already-empty buffer is a no-op — no Changed.
	_, cmd = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Nil(t, cmd, "Ctrl+U on empty must not broadcast Changed")
}

func TestPrompt_PrintableKeyEmitsChanged(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptFilter)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.NotNil(t, cmd)
	require.Equal(t,
		PromptChangedMsg{Mode: PromptFilter, Value: "a"},
		cmd())
}

func TestPrompt_RenderIncludesPrefix(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	p := NewPrompt().Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	out := testutil.StripStyle(p.Render(styles))
	require.Contains(t, out, "🐶>",
		"command mode renders the dog emoji + chevron, mirroring k9s")
	require.Contains(t, out, "s")

	p2 := NewPrompt().Open(PromptFilter)
	out2 := testutil.StripStyle(p2.Render(styles))
	require.Contains(t, out2, "🐩>",
		"filter mode renders the poodle emoji + chevron, mirroring k9s")
}

func TestPrompt_RenderClosedIsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, NewPrompt().Render(loadStyles(t)))
}

func TestPrompt_RenderHasNoBackgroundFill(t *testing.T) {
	t.Parallel()

	// The prompt sits inside an unstyled panel.RenderFrame above the
	// body. If we paint the prompt's palette bg behind the glyph it
	// renders as a coloured stripe in an otherwise transparent
	// frame — see the regression report. Lock it in: the rendered
	// SGR sequence must carry a foreground (38;) but no background
	// (48;) parameter.
	styles := loadStyles(t)

	for _, mode := range []PromptMode{PromptCommand, PromptFilter} {
		p := NewPrompt().Open(mode)
		out := p.Render(styles)
		require.Contains(t, out, "\x1b[38;",
			"prompt must still paint its foreground colour")
		require.NotContains(t, out, "\x1b[48;",
			"prompt must not paint a background colour — the surrounding frame is unstyled")
		require.NotContains(t, out, ";48;",
			"prompt must not paint a background colour even when chained with fg in one SGR")
	}
}

// ----- flash -----

func TestFlash_NewIsInactive(t *testing.T) {
	t.Parallel()

	f := NewFlash()
	require.False(t, f.IsActive())
	require.Empty(t, f.Render(loadStyles(t)))
}

func TestFlash_ShowAndAutoClear(t *testing.T) {
	t.Parallel()

	f := NewFlash()
	f, cmd := f.Update(FlashShowMsg{Level: FlashSuccess, Text: "saved"})
	require.True(t, f.IsActive())
	require.Equal(t, "saved", f.Text())
	require.NotNil(t, cmd, "Show must schedule the auto-clear tick")

	// The cmd is a tea.Tick which we can't easily fire deterministically
	// in a unit test without a clock. Instead, inject the clear message
	// directly with the matching id and verify the flash clears.
	msg := flashClearMsg{id: 1} // first generation
	f, _ = f.Update(msg)
	require.False(t, f.IsActive(), "matching clear-msg must clear the flash")
}

func TestFlash_StaleClearIgnored(t *testing.T) {
	t.Parallel()

	f := NewFlash()

	// First show → generation 1
	f, _ = f.Update(FlashShowMsg{Level: FlashInfo, Text: "first"})

	// Second show before the first's clear arrives → generation 2
	f, _ = f.Update(FlashShowMsg{Level: FlashInfo, Text: "second"})

	// Stale clear from generation 1 must NOT clear the second flash.
	f, _ = f.Update(flashClearMsg{id: 1})
	require.True(t, f.IsActive(),
		"stale clear (id=1) must not clear a newer flash (id=2)")
	require.Equal(t, "second", f.Text())
	require.Contains(t, testutil.StripStyle(f.Render(loadStyles(t))), "second",
		"render must reflect the newer flash text, not the stale one")

	// Matching clear (gen 2) does clear it.
	f, _ = f.Update(flashClearMsg{id: 2})
	require.False(t, f.IsActive())
}

func TestFlash_RenderUsesLevelStyle(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)

	cases := []struct {
		name  string
		level FlashLevel
	}{
		{name: "success", level: FlashSuccess},
		{name: "info", level: FlashInfo},
		{name: "warn", level: FlashWarn},
		{name: "error", level: FlashError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewFlash()
			f, _ = f.Update(FlashShowMsg{Level: tc.level, Text: "msg"})
			out := f.Render(styles)
			require.NotEmpty(t, out)
			require.Contains(t, testutil.StripStyle(out), "msg")
		})
	}
}

func TestFlash_TTLOverride(t *testing.T) {
	t.Parallel()

	f := NewFlash()
	// TTL is consumed by the tea.Tick scheduling; we verify via the
	// returned cmd's existence (couldn't inspect Tick internals).
	_, cmd := f.Update(FlashShowMsg{Level: FlashError, Text: "boom", TTL: time.Second})
	require.NotNil(t, cmd, "TTL override still schedules a clear tick")
}

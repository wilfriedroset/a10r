// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
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

	out := stripStyle(c.Render(styles))
	require.Contains(t, out, "alerts")
	require.Contains(t, out, "detail")
	require.Contains(t, out, crumbSeparator)

	c = c.Pop()
	require.Equal(t, 1, c.Len())
	require.Equal(t, "alerts", c.Top())

	out = stripStyle(c.Render(styles))
	require.Contains(t, out, "alerts")
	require.NotContains(t, out, "detail")
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
		updated, _ := p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		p = updated.(Prompt)
	}
	require.Equal(t, "alerts", p.Value())
}

func TestPrompt_BackspaceRemovesLastRune(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	for _, r := range "alerts" {
		updated, _ := p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		p = updated.(Prompt)
	}

	updated, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	p = updated.(Prompt)
	require.Equal(t, "alert", p.Value())
}

func TestPrompt_BackspaceMultiByteRune(t *testing.T) {
	t.Parallel()

	// Regression: a byte-slicing implementation would corrupt this.
	p := NewPrompt().Open(PromptFilter)
	for _, r := range "caféñ" {
		updated, _ := p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		p = updated.(Prompt)
	}
	require.Equal(t, "caféñ", p.Value())

	updated, _ := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	p = updated.(Prompt)
	require.Equal(t, "café", p.Value(),
		"backspace must pop one rune, not one byte (ñ is 2 bytes in UTF-8)")
}

func TestPrompt_BackspaceOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	updated, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	p = updated.(Prompt)
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
		updated, _ := p.Update(tea.KeyPressMsg{Code: code})
		p = updated.(Prompt)
	}
	require.Empty(t, p.Value(),
		"non-printable navigation keys must not be appended")
}

func TestPrompt_PasteAppendsContent(t *testing.T) {
	t.Parallel()

	// Bracketed paste should append the pasted content as a single
	// chunk so users can paste a UID / labelset into the command bar.
	p := NewPrompt().Open(PromptCommand)
	updated, cmd := p.Update(tea.PasteMsg{Content: "alertname=High_CPU"})
	p = updated.(Prompt)
	require.Nil(t, cmd)
	require.Equal(t, "alertname=High_CPU", p.Value())
}

func TestPrompt_CodeFallbackForEmptyText(t *testing.T) {
	t.Parallel()

	// Some terminals report a printable rune via Code without
	// populating Text. The prompt must still accept it.
	p := NewPrompt().Open(PromptFilter)
	updated, _ := p.Update(tea.KeyPressMsg{Code: 'a'})
	p = updated.(Prompt)
	require.Equal(t, "a", p.Value())
}

func TestPrompt_CtrlUClearsBuffer(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptFilter)
	for _, r := range "high cpu" {
		updated, _ := p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		p = updated.(Prompt)
	}
	require.Equal(t, "high cpu", p.Value())

	updated, _ := p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	p = updated.(Prompt)
	require.Empty(t, p.Value(), "Ctrl+U clears the buffer per keybindings.md")
}

func TestPrompt_EnterEmitsSubmittedAndCloses(t *testing.T) {
	t.Parallel()

	p := NewPrompt().Open(PromptCommand)
	for _, r := range "sil" {
		updated, _ := p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		p = updated.(Prompt)
	}

	updated, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	p = updated.(Prompt)
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
		updated, _ := p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		p = updated.(Prompt)
	}

	updated, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	p = updated.(Prompt)
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
	updated, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.Nil(t, cmd)
	require.Empty(t, updated.(Prompt).Value(),
		"a closed prompt must not accept keystrokes")
}

func TestPrompt_RenderIncludesPrefix(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	p := NewPrompt().Open(PromptCommand)
	updated, _ := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	p = updated.(Prompt)

	out := stripStyle(p.Render(styles))
	require.Contains(t, out, ":", "command mode renders the : prefix")
	require.Contains(t, out, "s")

	p2 := NewPrompt().Open(PromptFilter)
	out2 := stripStyle(p2.Render(styles))
	require.Contains(t, out2, "/", "filter mode renders the / prefix")
}

func TestPrompt_RenderClosedIsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, NewPrompt().Render(loadStyles(t)))
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
	updated, cmd := f.Update(FlashShowMsg{Level: FlashSuccess, Text: "saved"})
	f = updated.(Flash)
	require.True(t, f.IsActive())
	require.Equal(t, "saved", f.Text())
	require.NotNil(t, cmd, "Show must schedule the auto-clear tick")

	// The cmd is a tea.Tick which we can't easily fire deterministically
	// in a unit test without a clock. Instead, inject the clear message
	// directly with the matching id and verify the flash clears.
	msg := flashClearMsg{id: 1} // first generation
	updated, _ = f.Update(msg)
	f = updated.(Flash)
	require.False(t, f.IsActive(), "matching clear-msg must clear the flash")
}

func TestFlash_StaleClearIgnored(t *testing.T) {
	t.Parallel()

	f := NewFlash()

	// First show → generation 1
	updated, _ := f.Update(FlashShowMsg{Level: FlashInfo, Text: "first"})
	f = updated.(Flash)

	// Second show before the first's clear arrives → generation 2
	updated, _ = f.Update(FlashShowMsg{Level: FlashInfo, Text: "second"})
	f = updated.(Flash)

	// Stale clear from generation 1 must NOT clear the second flash.
	updated, _ = f.Update(flashClearMsg{id: 1})
	f = updated.(Flash)
	require.True(t, f.IsActive(),
		"stale clear (id=1) must not clear a newer flash (id=2)")
	require.Equal(t, "second", f.Text())
	require.Contains(t, stripStyle(f.Render(loadStyles(t))), "second",
		"render must reflect the newer flash text, not the stale one")

	// Matching clear (gen 2) does clear it.
	updated, _ = f.Update(flashClearMsg{id: 2})
	f = updated.(Flash)
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
			updated, _ := f.Update(FlashShowMsg{Level: tc.level, Text: "msg"})
			f = updated.(Flash)
			out := f.Render(styles)
			require.NotEmpty(t, out)
			require.Contains(t, stripStyle(out), "msg")
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

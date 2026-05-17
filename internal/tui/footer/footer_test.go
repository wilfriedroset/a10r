// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// ----- crumbs -----

func TestCrumbs_PushPopRender(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)
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
	require.NotEqual(t, "mutated", c.Render(testutil.LoadStyles(t)),
		"Set must copy the input slice so external mutation doesn't bleed in")
}

// ----- prompt -----

func TestPrompt_BackspaceRemovesLastRune(t *testing.T) {
	t.Parallel()

	p := NewPrompt(nil).Open(PromptCommand)
	for _, r := range "alerts" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "alert", p.Value())
}

func TestPrompt_BackspaceMultiByteRune(t *testing.T) {
	t.Parallel()

	// Regression: a byte-slicing implementation would corrupt this.
	p := NewPrompt(nil).Open(PromptFilter)
	for _, r := range "caféñ" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "caféñ", p.Value())

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "café", p.Value(),
		"backspace must pop one rune, not one byte (ñ is 2 bytes in UTF-8)")
}

func TestPrompt_NonPrintableKeysIgnored(t *testing.T) {
	t.Parallel()

	// Arrow keys, F-keys, and modifier-only events leave Text empty
	// and have non-printable Codes — they must not corrupt the buffer.
	p := NewPrompt(nil).Open(PromptCommand)
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
	p := NewPrompt(nil).Open(PromptCommand)
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
	p := NewPrompt(nil).Open(PromptFilter)
	p, _ = p.Update(tea.KeyPressMsg{Code: 'a'})
	require.Equal(t, "a", p.Value())
}

func TestPrompt_EnterEmitsSubmittedAndCloses(t *testing.T) {
	t.Parallel()

	p := NewPrompt(nil).Open(PromptCommand)
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

	p := NewPrompt(nil).Open(PromptFilter)
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

	p := NewPrompt(nil)
	require.False(t, p.IsOpen())
	p, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.Nil(t, cmd)
	require.Empty(t, p.Value(),
		"a closed prompt must not accept keystrokes")
}

func TestPrompt_BackspaceEmitsChanged(t *testing.T) {
	t.Parallel()

	p := NewPrompt(nil).Open(PromptFilter)
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
	p := NewPrompt(nil).Open(PromptFilter)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Nil(t, cmd, "no-op backspace must not broadcast Changed")
}

func TestPrompt_CtrlUEmitsChangedOnce(t *testing.T) {
	t.Parallel()

	p := NewPrompt(nil).Open(PromptFilter)
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

	p := NewPrompt(nil).Open(PromptFilter)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.NotNil(t, cmd)
	require.Equal(t,
		PromptChangedMsg{Mode: PromptFilter, Value: "a"},
		cmd())
}

func TestPrompt_RenderIncludesPrefix(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)
	p := NewPrompt(nil).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	out := testutil.StripStyle(p.Render(styles))
	require.Contains(t, out, "🐶>",
		"command mode renders the dog emoji + chevron, mirroring k9s")
	require.Contains(t, out, "s")

	p2 := NewPrompt(nil).Open(PromptFilter)
	out2 := testutil.StripStyle(p2.Render(styles))
	require.Contains(t, out2, "🐩>",
		"filter mode renders the poodle emoji + chevron, mirroring k9s")
}

func TestPrompt_RenderClosedIsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, NewPrompt(nil).Render(testutil.LoadStyles(t)))
}

func TestPrompt_RenderHasNoBackgroundFill(t *testing.T) {
	t.Parallel()

	// The prompt sits inside an unstyled panel.RenderFrame above the
	// body. If we paint the prompt's palette bg behind the glyph it
	// renders as a coloured stripe in an otherwise transparent
	// frame — see the regression report. Lock it in: the rendered
	// SGR sequence must carry a foreground (38;) but no background
	// (48;) parameter. Bold (1;) on the typed segment may precede
	// the fg in the chained SGR — accept either standalone or
	// chained fg openings.
	styles := testutil.LoadStyles(t)

	for _, mode := range []PromptMode{PromptCommand, PromptFilter} {
		p := NewPrompt(nil).Open(mode)
		out := p.Render(styles)
		fgChained := strings.Contains(out, "\x1b[38;") ||
			strings.Contains(out, ";38;")
		require.True(t, fgChained,
			"prompt must still paint its foreground colour")
		require.NotContains(t, out, "\x1b[48;",
			"prompt must not paint a background colour — the surrounding frame is unstyled")
		require.NotContains(t, out, ";48;",
			"prompt must not paint a background colour even when chained with fg in one SGR")
	}
}

// ----- prompt: ghost-text completion -----

// stubSuggester returns a function that maps an exact input to a
// canned suggestion. Anything not in the map returns "" — matches
// the cmdbar.Resolver.Suggest contract.
func stubSuggester(t *testing.T, m map[string]string) func(string) string {
	t.Helper()
	return func(in string) string { return m[in] }
}

func TestPrompt_SuggestionOnCommandTextChange(t *testing.T) {
	t.Parallel()

	sug := stubSuggester(t, map[string]string{
		"s":  "sil",
		"si": "sil",
	})
	p := NewPrompt(sug).Open(PromptCommand)
	require.Empty(t, p.Suggestion(), "fresh prompt has no suggestion")

	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Equal(t, "sil", p.Suggestion(),
		"command-mode keystroke recomputes the suggestion")

	p, _ = p.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	require.Equal(t, "sil", p.Suggestion())
}

func TestPrompt_SuggestionSkipsFilterMode(t *testing.T) {
	t.Parallel()

	// Filter mode doesn't surface ghost text in this iteration; the
	// suggester is not consulted regardless of what it would return.
	sug := stubSuggester(t, map[string]string{"s": "sil"})
	p := NewPrompt(sug).Open(PromptFilter)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Empty(t, p.Suggestion(),
		"filter mode must not invoke the command-mode suggester")
}

func TestPrompt_NilSuggesterIsSafe(t *testing.T) {
	t.Parallel()

	// Wizard / headless flows construct Prompt with no suggester.
	// A nil dep must degrade gracefully to "no ghost ever".
	p := NewPrompt(nil).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Empty(t, p.Suggestion())
}

func TestPrompt_BackspaceRecomputesSuggestion(t *testing.T) {
	t.Parallel()

	sug := stubSuggester(t, map[string]string{
		"s":   "sil",
		"si":  "sil",
		"sil": "",
	})
	p := NewPrompt(sug).Open(PromptCommand)
	for _, r := range "sil" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Empty(t, p.Suggestion(),
		"exact alias match leaves no ghost (cmdbar.Suggest contract)")

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "sil", p.Suggestion(),
		"backspace must recompute the suggestion against the new buffer")
}

func TestPrompt_PasteRecomputesSuggestion(t *testing.T) {
	t.Parallel()

	sug := stubSuggester(t, map[string]string{"silen": "silences"})
	p := NewPrompt(sug).Open(PromptCommand)
	p, _ = p.Update(tea.PasteMsg{Content: "silen"})
	require.Equal(t, "silences", p.Suggestion(),
		"paste must recompute the suggestion against the post-paste buffer")
}

func TestPrompt_OpenClearsPriorSuggestion(t *testing.T) {
	t.Parallel()

	// Re-opening the prompt after a previous session must reset
	// state; a stale ghost from the prior open would mislead.
	sug := stubSuggester(t, map[string]string{"s": "sil"})
	p := NewPrompt(sug).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Equal(t, "sil", p.Suggestion())

	p = p.Close().Open(PromptCommand)
	require.Empty(t, p.Suggestion())
}

func TestPrompt_CtrlFAcceptsSuggestion(t *testing.T) {
	t.Parallel()

	// Ctrl+F mirrors Tab so users coming from k9s keep the muscle
	// memory; both keys are otherwise unbound while the prompt is
	// open (the dispatcher's table-context Ctrl+F = page-down only
	// fires when the prompt is closed).
	sug := stubSuggester(t, map[string]string{"s": "sil"})
	p := NewPrompt(sug).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	p, cmd := p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, "sil ", p.Value())
	require.NotNil(t, cmd)
}

func TestPrompt_TabNoOpWithoutSuggestion(t *testing.T) {
	t.Parallel()

	// No suggestion → Tab is silent. We don't insert a literal `\t`
	// (would pollute the buffer) and we don't emit a Changed (would
	// trigger spurious downstream work for keystrokes that didn't
	// move the buffer, mirroring the existing no-op-backspace rule).
	sug := stubSuggester(t, map[string]string{})
	p := NewPrompt(sug).Open(PromptCommand)
	for _, r := range "xyz" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Empty(t, p.Suggestion())

	before := p.Value()
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, before, p.Value(),
		"Tab without ghost must not mutate the buffer")
	require.Nil(t, cmd, "Tab without ghost must not broadcast Changed")
}

func TestPrompt_TabInFilterModeIsNoOp(t *testing.T) {
	t.Parallel()

	// Filter mode never has a ghost (Q7a), so Tab is silent.
	// Forwarding Tab to the page from here would risk page-specific
	// surprises; the prompt swallows the key cleanly.
	sug := stubSuggester(t, map[string]string{"s": "sil"})
	p := NewPrompt(sug).Open(PromptFilter)
	for _, r := range "high" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	before := p.Value()
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, before, p.Value())
	require.Nil(t, cmd)
}

func TestPrompt_SuggesterContractViolationDropped(t *testing.T) {
	t.Parallel()

	// A misbehaving suggester that returns a string which doesn't
	// start with the buffer would render as a garbled overlay.
	// Defensively drop it — better no ghost than the wrong ghost.
	bad := func(_ string) string { return "nope" }
	p := NewPrompt(bad).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Empty(t, p.Suggestion(),
		"suggester returns must have the buffer as a prefix; bogus returns are dropped")
}

func TestPrompt_RenderShowsGhost(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)
	sug := stubSuggester(t, map[string]string{"s": "sil"})
	p := NewPrompt(sug).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	out := p.Render(styles)
	plain := testutil.StripStyle(out)
	require.Contains(t, plain, "sil",
		"ghost suffix renders directly after the typed value")
	require.NotContains(t, plain, "_",
		"underscore cursor mark is gone — bold-vs-plain is the boundary now")
	require.Contains(t, out, "\x1b[1",
		"typed segment must carry the bold SGR attribute (k9s parity)")
}

func TestPrompt_RenderHasNoBackgroundFillWithGhost(t *testing.T) {
	t.Parallel()

	// The ghost must obey the same fg-only rule as the rest of the
	// prompt: the surrounding panel.RenderFrame is unstyled, so a
	// painted bg behind the ghost would render as a coloured stripe.
	styles := testutil.LoadStyles(t)
	sug := stubSuggester(t, map[string]string{"s": "sil"})
	p := NewPrompt(sug).Open(PromptCommand)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	out := p.Render(styles)
	require.NotContains(t, out, "\x1b[48;",
		"ghost must not paint a background colour")
	require.NotContains(t, out, ";48;",
		"ghost must not paint a background colour even when chained with fg")
}

// ----- prompt + history -----

func TestPrompt_UpCyclesHistoryPrev(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("alerts")
	h.Append("silences")

	p := NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	for _, r := range "draft" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, "silences", p.Value(),
		"first Up must surface the newest history entry")
	require.NotNil(t, cmd, "history cycle must broadcast Changed so the page re-filters")
	require.Equal(t,
		PromptChangedMsg{Mode: PromptFilter, Value: "silences"},
		cmd())

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, "alerts", p.Value())
}

func TestPrompt_DownRestoresDraftAtPresent(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("alerts")

	p := NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	for _, r := range "wip" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, "alerts", p.Value())

	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "wip", p.Value(),
		"crossing past the newest must restore the pre-cycle draft")
	require.NotNil(t, cmd)
	require.Equal(t,
		PromptChangedMsg{Mode: PromptFilter, Value: "wip"},
		cmd())
}

func TestPrompt_TabPrefersGhostOverHistory(t *testing.T) {
	t.Parallel()

	// Tab with an active ghost-text completion accepts the ghost,
	// not the history. History cycling is the fallback when no
	// ghost is showing — surprising the user by cycling instead of
	// completing would shadow the obvious affordance.
	sug := stubSuggester(t, map[string]string{"s": "silences"})
	h := NewHistory("", HistoryCmd)
	h.Append("alerts")

	p := NewPrompt(sug).OpenWithHistory(PromptCommand, h)
	p, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Equal(t, "silences", p.Suggestion())

	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "silences ", p.Value(),
		"Tab with a live ghost accepts the suggestion, not a history walk")
}

func TestPrompt_TabFallsThroughToHistoryWhenNoGhost(t *testing.T) {
	t.Parallel()

	// Empty buffer → no ghost → Tab should walk history backward.
	h := NewHistory("", HistoryCmd)
	h.Append("alerts")

	p := NewPrompt(nil).OpenWithHistory(PromptCommand, h)
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "alerts", p.Value(),
		"Tab without a ghost must fall through to history cycling")
}

func TestPrompt_SubmitAppendsToHistory(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	p := NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	for _, r := range "high" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, 1, h.Len(),
		"Enter must commit the buffer to the attached history ring")

	// Second prompt session: Up surfaces the last submission.
	p = NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, "high", p.Value())
}

func TestPrompt_EscDoesNotAppendToHistory(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	p := NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	for _, r := range "throwaway" {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Zero(t, h.Len(),
		"Esc bails — the buffer must not pollute the history ring")
}

func TestPrompt_SubmitEmptyDoesNotAppendToHistory(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	p := NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Zero(t, h.Len(),
		"submitting an empty buffer is the user's escape hatch — must not pollute history")
}

func TestPrompt_NilHistoryNeverPanics(t *testing.T) {
	t.Parallel()

	// Up/Down/Tab on a prompt opened without a history (legacy
	// Open()) must be quiet no-ops, not panics.
	p := NewPrompt(nil).Open(PromptFilter)
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Empty(t, p.Value())
	require.Nil(t, cmd)

	p, cmd = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Empty(t, p.Value())
	require.Nil(t, cmd)
}

func TestPrompt_OpenResetsHistoryCycle(t *testing.T) {
	t.Parallel()

	// A pre-cycled history (from a prior prompt session that walked
	// some entries) must rewind to "not cycling" when a fresh
	// prompt opens — the user expects Up to start at the newest,
	// not wherever the previous session left the cursor.
	h := NewHistory("", HistoryFilter)
	h.Append("a")
	h.Append("b")
	_, _ = h.Prev("")
	require.True(t, h.Cycling())

	_ = NewPrompt(nil).OpenWithHistory(PromptFilter, h)
	require.False(t, h.Cycling(),
		"OpenWithHistory must Reset the ring so the cursor starts at present")
}

// ----- flash -----

func TestFlash_NewIsInactive(t *testing.T) {
	t.Parallel()

	f := NewFlash()
	require.False(t, f.IsActive())
	require.Empty(t, f.Render(testutil.LoadStyles(t)))
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
	require.Contains(t, testutil.StripStyle(f.Render(testutil.LoadStyles(t))), "second",
		"render must reflect the newer flash text, not the stale one")

	// Matching clear (gen 2) does clear it.
	f, _ = f.Update(flashClearMsg{id: 2})
	require.False(t, f.IsActive())
}

func TestFlash_RenderUsesLevelStyle(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)

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

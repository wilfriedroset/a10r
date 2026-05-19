// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// newAppWithCmdbar builds an App wired to a populated resolver.
// The :alerts and :status aliases push fakePages so tests can
// observe the round-trip through the program loop.
func newAppWithCmdbar(t *testing.T) (*App, *fakePage) {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	page := newFakePage("alerts")
	resolver := cmdbar.New()
	resolver.Register("alerts", func(_ []string) tea.Cmd {
		return PushPage(func() Page { return page })
	})
	resolver.Register("status", func(_ []string) tea.Cmd {
		return PushPage(func() Page { return newFakePage("status") })
	})
	resolver.Register("q", func(_ []string) tea.Cmd { return tea.Quit })
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		CmdBar:     resolver,
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)
	return a, page
}

func TestCmdBar_ColonOpensCommandPrompt(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	require.True(t, a.prompt.IsOpen())
	require.Equal(t, footer.PromptCommand, a.prompt.Mode())
}

func TestCmdBar_SlashOpensFilterPrompt(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	updated, _ := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	require.True(t, a.prompt.IsOpen())
	require.Equal(t, footer.PromptFilter, a.prompt.Mode())
}

func TestCmdBar_AliasResolvesAndPushesPage(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)

	// Open the command bar via `:`, type "alerts", press Enter.
	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	for _, r := range "alerts" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.False(t, a.prompt.IsOpen(), "submit must close the prompt")
	require.Same(t, page, a.topPage(),
		"resolved :alerts must push the alerts page")
}

func TestCmdBar_PrefixResolvesToFullAlias(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	for _, r := range "stat" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.NotNil(t, a.topPage())
	require.Equal(t, "status", a.topPage().Crumb(),
		":stat must resolve to :status via unique-prefix match")
}

func TestCmdBar_UnknownAliasFlashesWarn(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	for _, r := range "nope" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.True(t, a.flash.IsActive(), "unknown command must surface a flash")
	require.Contains(t, a.flash.Text(), "nope")
}

func TestCmdBar_AmbiguousAliasFlashesWarn(t *testing.T) {
	t.Parallel()
	// Build a resolver where two aliases share a prefix.
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	resolver := cmdbar.New()
	resolver.Register("status", func(_ []string) tea.Cmd { return nil })
	resolver.Register("silences", func(_ []string) tea.Cmd { return nil })
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		CmdBar:     resolver,
	})
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	updated, _ = a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	updated, _ = a.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	a = updated.(*App)
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.True(t, a.flash.IsActive())
	require.Contains(t, a.flash.Text(), "ambiguous")
}

func TestCmdBar_QuitsViaColonQ(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	updated, _ = a.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	a = updated.(*App)
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)

	// The prompt's Cmd emits PromptSubmittedMsg; running it returns
	// the cmdbar handler's tea.Quit Cmd.
	msg := cmd()
	require.IsType(t, footer.PromptSubmittedMsg{}, msg)
	_, cmd = a.Update(msg)
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

func TestCmdBar_FilterSubmitForwardsToPage(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)
	drive(t, a, PushPage(func() Page { return page }))

	// Open `/`, type "severity=critical", submit.
	updated, _ := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	for _, r := range "abc" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	// Last message routed to the page must be the PromptSubmittedMsg.
	require.NotEmpty(t, *page.updateLog)
	last := (*page.updateLog)[len(*page.updateLog)-1]
	sub, ok := last.(footer.PromptSubmittedMsg)
	require.True(t, ok)
	require.Equal(t, footer.PromptFilter, sub.Mode)
	require.Equal(t, "abc", sub.Value)
}

func TestCmdBar_FilterCancelForwardsToPage(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)
	drive(t, a, PushPage(func() Page { return page }))

	updated, _ := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)

	// Page must see PromptCancelledMsg{Mode: PromptFilter}.
	require.NotEmpty(t, *page.updateLog)
	last := (*page.updateLog)[len(*page.updateLog)-1]
	cancel, ok := last.(footer.PromptCancelledMsg)
	require.True(t, ok)
	require.Equal(t, footer.PromptFilter, cancel.Mode)
}

func TestCmdBar_RepressColonWhilePromptOpen(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	// Open `:`, then type `:` again — the second `:` must reach the
	// prompt as a literal character, not re-trigger the dispatcher.
	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	updated, _ = a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)

	require.True(t, a.prompt.IsOpen())
	require.Equal(t, footer.PromptCommand, a.prompt.Mode())
	require.Equal(t, ":", a.prompt.Value(),
		"second `:` must extend the buffer, not re-trigger open")
}

func TestCmdBar_CancelThenReopenIsClean(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	// Open `:`, type "junk", Esc, reopen with `/`. The new prompt
	// must come up empty in PromptFilter mode — no leakage from
	// the previous session.
	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	for _, r := range "junk" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)
	require.False(t, a.prompt.IsOpen())

	updated, cmd = a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	drive(t, a, cmd)
	require.True(t, a.prompt.IsOpen())
	require.Equal(t, footer.PromptFilter, a.prompt.Mode())
	require.Empty(t, a.prompt.Value(),
		"reopen must come up clean — no buffer leak")
}

func TestCmdBar_FilterOpenForwardsOpenedMsgToPage(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)
	drive(t, a, PushPage(func() Page { return page }))
	logLenBefore := len(*page.updateLog)

	updated, cmd := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	drive(t, a, cmd)

	// Some message after the open key must be a PromptOpenedMsg.
	var seenOpen bool
	for _, msg := range (*page.updateLog)[logLenBefore:] {
		if op, ok := msg.(footer.PromptOpenedMsg); ok && op.Mode == footer.PromptFilter {
			seenOpen = true
			break
		}
	}
	require.True(t, seenOpen,
		"opening `/` must forward PromptOpenedMsg{Mode: PromptFilter} so pages can snapshot")
}

func TestCmdBar_CommandOpenDoesNotForwardOpenedMsg(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)
	drive(t, a, PushPage(func() Page { return page }))
	logLenBefore := len(*page.updateLog)

	updated, cmd := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	drive(t, a, cmd)

	for _, msg := range (*page.updateLog)[logLenBefore:] {
		_, ok := msg.(footer.PromptOpenedMsg)
		require.False(t, ok,
			"`:` is owned by cmdbar; pages must NOT receive PromptOpenedMsg for command mode")
	}
}

func TestCmdBar_EmptyCommandIsSilent(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.False(t, a.prompt.IsOpen())
	require.False(t, a.flash.IsActive(),
		"empty `:` Enter must be silent — no flash about an unsubmitted command")
}

func TestCmdBar_TabAcceptsGhostCompletion(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)

	// Open `:`, type a unique-prefix `a` (matches only `alerts`).
	// The prompt should ghost the rest. Pressing Tab promotes the
	// ghost to the buffer; Enter then resolves and pushes the page.
	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	updated, _ = a.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	a = updated.(*App)
	require.Equal(t, "alerts", a.prompt.Suggestion(),
		"`:a` matches only `alerts`; the resolver must surface it as a ghost")

	updated, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	a = updated.(*App)
	require.Equal(t, "alerts ", a.prompt.Value(),
		"Tab promotes the full alias to the buffer with a trailing space")
	require.Empty(t, a.prompt.Suggestion(),
		"Tab clears the ghost; the resuggester returns \"\" for the post-Tab buffer")

	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)
	require.Same(t, page, a.topPage(),
		"submit after Tab acceptance must resolve and push the same page as a manual `:alerts`")
}

func TestCmdBar_TabInFilterModeIsNoOp(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)
	drive(t, a, PushPage(func() Page { return page }))

	// `/` filter mode has no completion source in this iteration;
	// Tab must be a silent no-op — neither corrupting the buffer
	// nor leaking through to the page (which would risk page-
	// specific Tab handling firing while the prompt is open).
	updated, _ := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	updated, _ = a.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	a = updated.(*App)
	before := a.prompt.Value()
	logLenBefore := len(*page.updateLog)

	updated, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	a = updated.(*App)
	require.Equal(t, before, a.prompt.Value(),
		"filter-mode Tab must not mutate the buffer")
	require.Empty(t, a.prompt.Suggestion())
	for _, msg := range (*page.updateLog)[logLenBefore:] {
		_, isKey := msg.(tea.KeyPressMsg)
		require.False(t, isKey,
			"filter-mode Tab must NOT be forwarded to the page")
	}
}

func TestApp_HistoryFor_CommandModeAlwaysCmd(t *testing.T) {
	t.Parallel()

	h := newAppHistories("")
	require.Same(t, h.cmd, h.historyFor(footer.PromptCommand, "alerts"))
	require.Same(t, h.cmd, h.historyFor(footer.PromptCommand, "silences"))
	require.Same(t, h.cmd, h.historyFor(footer.PromptCommand, ""),
		"`:` always picks cmd-history regardless of which page is on top")
}

func TestApp_HistoryFor_FilterModeRoutesPerPage(t *testing.T) {
	t.Parallel()

	h := newAppHistories("")
	require.Same(t, h.silenceMatcher,
		h.historyFor(footer.PromptFilter, "silences"),
		"silences page's `/` walks the silence-matcher ring (Prom-style fields)")
	require.Same(t, h.filter, h.historyFor(footer.PromptFilter, "alerts"))
	require.Same(t, h.filter, h.historyFor(footer.PromptFilter, "groups"))
	require.Same(t, h.filter, h.historyFor(footer.PromptFilter, ""),
		"unknown / no top page falls back to the generic filter ring")
}

func TestCmdBar_FilterCyclePersistsAcrossSessions(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	// Submit `/foo`. The filter ring should pick that up and a
	// re-opened `/` prompt with Up should surface "foo".
	updated, _ := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	for _, r := range "foo" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	// Re-open `/` and press Up. The ring is the same instance the
	// App holds, so Up surfaces "foo" without disk I/O.
	updated, cmd = a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	drive(t, a, cmd)
	updated, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	a = updated.(*App)
	require.Equal(t, "foo", a.prompt.Value(),
		"a fresh `/` prompt must walk the same ring the previous submission populated")
}

func TestCmdBar_SilencesFilterUsesSeparateRing(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	// Push a fake silences page (crumb = "silences") and submit a
	// filter. The submission must land in the silence-matcher ring,
	// not the generic filter ring — typing alerts-flavoured queries
	// on the silences page would otherwise leak into the alerts
	// page's recall set on a future session, which is the whole
	// point of having three classes.
	silencesPage := newFakePage("silences")
	drive(t, a, PushPage(func() Page { return silencesPage }))

	updated, cmd := a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	drive(t, a, cmd)
	for _, r := range "creator=bob" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd = a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	require.Equal(t, 1, a.histories.silenceMatcher.Len(),
		"silences page submission must land in silence-matcher ring")
	require.Zero(t, a.histories.filter.Len(),
		"the generic filter ring must be untouched")
}

func TestCmdBar_CommandRingIndependentOfFilterRing(t *testing.T) {
	t.Parallel()
	a, _ := newAppWithCmdbar(t)

	// Submit `:alerts`, then open `/`. Up on the filter prompt must
	// NOT surface "alerts" — the rings are independent classes.
	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	for _, r := range "alerts" {
		updated, _ = a.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		a = updated.(*App)
	}
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	a = updated.(*App)
	drive(t, a, cmd)

	updated, cmd = a.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	a = updated.(*App)
	drive(t, a, cmd)
	updated, _ = a.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	a = updated.(*App)
	require.Empty(t, a.prompt.Value(),
		"`:` and `/` rings must not cross-pollinate")
}

func TestCmdBar_CommandCancelDoesNotReachPage(t *testing.T) {
	t.Parallel()
	a, page := newAppWithCmdbar(t)
	drive(t, a, PushPage(func() Page { return page }))
	logLenBefore := len(*page.updateLog)

	updated, _ := a.Update(tea.KeyPressMsg{Code: ':', Text: ":"})
	a = updated.(*App)
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)

	// Command-mode cancellations terminate at the App; the page
	// should NOT have received a PromptCancelledMsg.
	for _, msg := range (*page.updateLog)[logLenBefore:] {
		_, isCancel := msg.(footer.PromptCancelledMsg)
		require.False(t, isCancel,
			"command-mode cancel must NOT reach the page")
	}
}

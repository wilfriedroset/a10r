// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
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
		Styles:     *styles,
		Registry:   action.New(),
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
		Styles:     *styles,
		Registry:   action.New(),
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

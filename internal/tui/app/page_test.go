// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// fakePage is a minimal Page implementation for stack-mechanics
// tests. It records every Update / Close call so assertions can
// verify message routing went to the right slot and tear-down ran
// at the right time.
type fakePage struct {
	name        string
	headerLabel string
	hints       []action.Action
	bodyText    string

	// initCmd is returned from Init so tests can verify cmd-chain
	// plumbing (a page's Init kicking off a poll etc.).
	initCmd tea.Cmd

	// closeCmd is returned from Close so tests can verify the app
	// shell propagates tear-down work to bubbletea's program loop.
	closeCmd tea.Cmd

	// Recorded state — pointers so derivative copies share the
	// underlying counters.
	initCalls  *int
	closeCalls *int
	updateLog  *[]tea.Msg
}

func newFakePage(name string) *fakePage {
	var (
		initCalls  int
		closeCalls int
		updateLog  []tea.Msg
	)
	return &fakePage{
		name:       name,
		bodyText:   name + " body",
		initCalls:  &initCalls,
		closeCalls: &closeCalls,
		updateLog:  &updateLog,
	}
}

func (p *fakePage) Init() tea.Cmd {
	*p.initCalls++
	return p.initCmd
}

func (p *fakePage) Close() tea.Cmd {
	*p.closeCalls++
	return p.closeCmd
}

func (p *fakePage) Update(msg tea.Msg) (Page, tea.Cmd) {
	*p.updateLog = append(*p.updateLog, msg)
	return p, nil
}

func (p *fakePage) View(_, _ int) string      { return p.bodyText }
func (p *fakePage) Crumb() string             { return p.name }
func (p *fakePage) HeaderContent() string     { return p.headerLabel }
func (p *fakePage) Bindings() []action.Action { return p.hints }

// drive runs tea.Cmds inside the App's Update loop the same way
// bubbletea's runtime would, draining the cmd queue depth-first
// until empty. Used to apply factory-typed messages (pushPageMsg,
// popPageMsg) and chained Init / Close cmds inside a test without
// booting tea.NewProgram.
//
// Time-sensitive Cmds (tea.Tick et al.) are not supported — they
// would block the test on a wall-clock wait. The page stack tests
// only use immediate Cmds, which makes the worklist a finite walk.
func drive(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cmd, queue = queue[len(queue)-1], queue[:len(queue)-1]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if msg == nil {
			continue
		}
		updated, next := a.Update(msg)
		require.Same(t, a, updated, "Update must return the same App pointer")
		queue = append(queue, next)
	}
}

func TestStack_PushRunsInit(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	require.Equal(t, 1, *page.initCalls,
		"Init must run exactly once on push")
	require.Same(t, page, a.topPage())
}

func TestStack_PushPushPopOrdering(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	alerts := newFakePage("alerts")
	detail := newFakePage("alert-detail")
	drive(t, a, PushPage(func() Page { return alerts }))
	drive(t, a, PushPage(func() Page { return detail }))

	require.Same(t, detail, a.topPage())
	require.Len(t, a.stack, 2)

	drive(t, a, PopPage())
	require.Same(t, alerts, a.topPage(),
		"pop must surface the page underneath")
	require.Len(t, a.stack, 1)
}

func TestStack_PopOnSingleStackIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	home := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return home }))

	drive(t, a, PopPage())
	require.Same(t, home, a.topPage(),
		"popping the home page must be a no-op so Esc on root never blanks the screen")
}

func TestStack_PopOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	drive(t, a, PopPage())
	require.Empty(t, a.stack)
	require.Nil(t, a.topPage())
}

func TestStack_Replace(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	first := newFakePage("first")
	second := newFakePage("second")
	drive(t, a, PushPage(func() Page { return first }))
	drive(t, a, ReplacePage(func() Page { return second }))

	require.Len(t, a.stack, 1, "replace must NOT grow the stack")
	require.Same(t, second, a.topPage())
	require.Equal(t, 1, *second.initCalls,
		"the replacement page's Init must run")
}

func TestStack_ReplaceOnEmptyFallsBackToPush(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	page := newFakePage("home")
	drive(t, a, ReplacePage(func() Page { return page }))

	require.Len(t, a.stack, 1)
	require.Same(t, page, a.topPage())
	require.Equal(t, 1, *page.initCalls)
}

func TestStack_EscPopsViaGlobalBinding(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	alerts := newFakePage("alerts")
	detail := newFakePage("alert-detail")
	drive(t, a, PushPage(func() Page { return alerts }))
	drive(t, a, PushPage(func() Page { return detail }))

	// Esc at the global layer must pop the top page.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)
	require.Same(t, alerts, a.topPage(),
		"Esc on a 2-deep stack must pop one level")
}

func TestStack_HeaderContentFromTopPage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	a = updated.(*App)

	page := newFakePage("alerts")
	page.headerLabel = "filter: severity=critical"
	page.hints = []action.Action{
		{Key: "s", Description: "silence"},
		{Key: "?", Description: "help"},
	}
	drive(t, a, PushPage(func() Page { return page }))

	visible := stripStyle(a.View().Content)
	require.Contains(t, visible, "filter: severity=critical",
		"top-of-stack page's HeaderContent must reach the header middle zone")
	require.Contains(t, visible, "[s]",
		"top-of-stack page's bindings must populate the hint strip")
}

func TestStack_CrumbsTrackStack(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	drive(t, a, PushPage(func() Page { return newFakePage("alerts") }))
	require.Equal(t, 1, a.crumbs.Len())
	require.Equal(t, "alerts", a.crumbs.Top())

	drive(t, a, PushPage(func() Page { return newFakePage("detail") }))
	require.Equal(t, 2, a.crumbs.Len())
	require.Equal(t, "detail", a.crumbs.Top())

	drive(t, a, PopPage())
	require.Equal(t, 1, a.crumbs.Len())
	require.Equal(t, "alerts", a.crumbs.Top())
}

func TestStack_BodyRendersTopPage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	a = updated.(*App)

	page := newFakePage("alerts")
	page.bodyText = "alerts list goes here"
	drive(t, a, PushPage(func() Page { return page }))

	require.Contains(t, a.View().Content, "alerts list goes here")
}

func TestStack_UnboundKeysReachTopPage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	// `j` is not bound at any layer in this test fixture. The app
	// must forward it to the page so vim motions etc. can register
	// at the page layer (or take effect via local handling).
	_, _ = a.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	require.NotEmpty(t, *page.updateLog,
		"unbound keys must reach the top page")
	require.IsType(t, tea.KeyPressMsg{}, (*page.updateLog)[len(*page.updateLog)-1])
}

func TestStack_MessagesForwardToTopPage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	type customMsg struct{ Tag string }
	_, _ = a.Update(customMsg{Tag: "data-tick"})

	require.NotEmpty(t, *page.updateLog,
		"custom messages must reach the page via the catch-all forward")
}

func TestStack_PushNilFactoryIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	_, _ = a.Update(pushPageMsg{Factory: nil})
	require.Empty(t, a.stack)
}

func TestStack_PushFactoryReturningNilIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	_, _ = a.Update(pushPageMsg{Factory: func() Page { return nil }})
	require.Empty(t, a.stack,
		"a factory returning nil must NOT push a nil page slot")
}

func TestStack_PopRunsCloseOnDeparting(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	alerts := newFakePage("alerts")
	detail := newFakePage("detail")
	drive(t, a, PushPage(func() Page { return alerts }))
	drive(t, a, PushPage(func() Page { return detail }))
	require.Zero(t, *detail.closeCalls)

	drive(t, a, PopPage())
	require.Equal(t, 1, *detail.closeCalls,
		"pop must Close the departing page so its background work tears down")
	require.Zero(t, *alerts.closeCalls,
		"the page surfaced by the pop must NOT be closed")
}

func TestStack_ReplaceRunsCloseBeforeNewInit(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	first := newFakePage("first")
	second := newFakePage("second")
	drive(t, a, PushPage(func() Page { return first }))
	drive(t, a, ReplacePage(func() Page { return second }))

	require.Equal(t, 1, *first.closeCalls,
		"replaced page must be Close()d so its background work doesn't leak")
	require.Equal(t, 1, *second.initCalls,
		"the new page's Init must run after the replace")
}

func TestStack_PopOnSingleStackDoesNotClose(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	home := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return home }))

	drive(t, a, PopPage())
	require.Zero(t, *home.closeCalls,
		"home page must NOT be closed when pop is a no-op")
}

func TestStack_EscOnOpenPromptDoesNotPop(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	updated, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = updated.(*App)

	alerts := newFakePage("alerts")
	detail := newFakePage("detail")
	drive(t, a, PushPage(func() Page { return alerts }))
	drive(t, a, PushPage(func() Page { return detail }))

	a.prompt = a.prompt.Open(footer.PromptCommand)

	// Esc with a prompt open dismisses the prompt; it must NOT pop
	// the page stack — that's the prompt-shadows-global rule from
	// keybindings.md.
	updated, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	a = updated.(*App)
	drive(t, a, cmd)

	require.False(t, a.prompt.IsOpen())
	require.Same(t, detail, a.topPage(),
		"Esc with prompt open must NOT pop the page stack")
}

func TestStack_PageInitChainedCmdsLand(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	// A page's Init returns a Cmd that itself emits a custom message.
	// drive must drain the chain so the page's Update sees the
	// follow-up message.
	type chainedMsg struct{}
	page := newFakePage("alerts")
	page.initCmd = func() tea.Msg { return chainedMsg{} }
	drive(t, a, PushPage(func() Page { return page }))

	require.NotEmpty(t, *page.updateLog,
		"chained Init Cmd must reach the page's Update")
	last := (*page.updateLog)[len(*page.updateLog)-1]
	require.IsType(t, chainedMsg{}, last)
}

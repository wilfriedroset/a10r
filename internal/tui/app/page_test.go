// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
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

	// capturesInput, when set, makes the page also implement the
	// InputCapturePage interface — used by tests that exercise
	// the dispatcher-bypass path for forms.
	capturesInput bool

	// Recorded state — pointers so derivative copies share the
	// underlying counters.
	initCalls  *int
	closeCalls *int
	updateLog  *[]tea.Msg
}

// CapturesInput satisfies the optional InputCapturePage
// interface; returns the configured flag so a test can opt in
// per-page.
func (p *fakePage) CapturesInput() bool { return p.capturesInput }

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
func (p *fakePage) Title() string             { return p.name }
func (p *fakePage) HeaderContent() string     { return p.headerLabel }
func (*fakePage) Footer() string              { return "" }
func (p *fakePage) Bindings() []action.Action { return p.hints }

// filteringFakePage embeds fakePage and adds the PollAwarePage
// implementation so the cache-replay filter has something to
// match. Defined here rather than as a fakePage flag because
// pages opt in by interface satisfaction — type-asserting a
// flag-bearing fakePage would always succeed and the filter
// path wouldn't be exercised the way production pages exercise it.
type filteringFakePage struct {
	*fakePage
	labels []string
}

// PollResources satisfies app.PollAwarePage.
func (p *filteringFakePage) PollResources() []string { return p.labels }

// drive runs tea.Cmds inside the App's Update loop the same way
// bubbletea's runtime would, draining the cmd queue depth-first
// until empty. Used to apply factory-typed messages (pushPageMsg,
// popPageMsg) and chained Init / Close cmds inside a test without
// booting tea.NewProgram.
//
// Time-sensitive Cmds (tea.Tick from flash auto-clear, poll
// scheduling) are skipped — drive runs each cmd in a goroutine
// with a small wall-clock budget; cmds that don't return in time
// are abandoned. Test setups that want to observe a tick must
// inject the resulting message directly.
func drive(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		cmd, queue = queue[len(queue)-1], queue[:len(queue)-1]
		if cmd == nil {
			continue
		}
		msg, ok := runWithBudget(cmd, 50*time.Millisecond)
		if !ok || msg == nil {
			continue
		}
		updated, next := a.Update(msg)
		require.Same(t, a, updated, "Update must return the same App pointer")
		queue = append(queue, next)
	}
}

// runWithBudget runs cmd in a goroutine and returns its message if
// it resolves within d; otherwise reports !ok and abandons the
// goroutine to finish on its own.
//
// The abandoned goroutine eventually completes (tea.Tick fires its
// timer, sends to the buffered channel, and exits) so the leak is
// bounded by the longest TTL × test count, not unbounded. With the
// flash TTL at 4s and ~20 cmdbar tests this peaks at ~80 parked
// goroutines for ~4s — well under the runtime's limits and
// invisible to -race within a normal test run.
func runWithBudget(cmd tea.Cmd, d time.Duration) (tea.Msg, bool) {
	ch := make(chan tea.Msg, 1) // buffered: abandoned goroutines never block
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg, true
	case <-time.After(d):
		return nil, false
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
		"top-of-stack page's HeaderContent must surface as a body subtitle")
	require.Contains(t, visible, "<s>",
		"top-of-stack page's bindings must populate the panel hint column")
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

// TestPollCache_HydratesNewlyPushedPage covers the snapshot-cache
// fast path: pollers fire before the user navigates, so a page
// pushed later must hydrate from the cached DataMsg rather than
// wait for the next tick. Without the replay the test page's
// updateLog would be empty until a fresh poll lands — sometimes
// up to a full minute in production. fakePage doesn't implement
// PollAwarePage so it gets every cached label; the filter
// behaviour is covered by TestPollCache_FilteringByPollResources
// below.
func TestPollCache_HydratesNewlyPushedPage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	// Simulate two pollers (alerts × prod, alerts × staging) that
	// have already published a tick before any page is pushed.
	now := time.Now()
	prodMsg := poll.DataMsg{
		Resource:      []string{"prod-alert"},
		Tenant:        "prod",
		ResourceLabel: "alerts",
		At:            now,
		NextAt:        now.Add(time.Minute),
	}
	stagingMsg := poll.DataMsg{
		Resource:      []string{"staging-alert"},
		Tenant:        "staging",
		ResourceLabel: "alerts",
		At:            now,
		NextAt:        now.Add(time.Minute),
	}
	a.Update(prodMsg)
	a.Update(stagingMsg)

	// Now push a page — its Update must receive both cached msgs
	// before any new tick arrives.
	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	// Two cached entries → two replays. Order isn't guaranteed
	// (map iteration), so assert by membership.
	tenants := map[string]bool{}
	for _, msg := range *page.updateLog {
		if dm, ok := msg.(poll.DataMsg); ok {
			tenants[dm.Tenant] = true
		}
	}
	require.True(t, tenants["prod"], "prod cached msg must reach page (log=%v)", *page.updateLog)
	require.True(t, tenants["staging"], "staging cached msg must reach page (log=%v)", *page.updateLog)
}

// TestPollCache_FilteringByPollResources covers the PollAwarePage
// extension: a page that opts in to filtering only sees cached
// payloads for the labels it declared. The reviewer flagged that
// the original replay-everything path was O(resources × tenants)
// busy work for every push; the extension trims it to just what
// the page reacts to.
func TestPollCache_FilteringByPollResources(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	// Cache one snapshot per resource label.
	a.Update(poll.DataMsg{Resource: []string{"a"}, Tenant: "prod", ResourceLabel: "alerts"})
	a.Update(poll.DataMsg{Resource: []string{"s"}, Tenant: "prod", ResourceLabel: "silences"})
	a.Update(poll.DataMsg{Resource: []string{"r"}, Tenant: "prod", ResourceLabel: "receivers"})

	// Push a page that only wants "silences".
	page := &filteringFakePage{fakePage: newFakePage("silences"), labels: []string{"silences"}}
	drive(t, a, PushPage(func() Page { return page }))

	var got []string
	for _, msg := range *page.updateLog {
		if dm, ok := msg.(poll.DataMsg); ok {
			got = append(got, dm.ResourceLabel)
		}
	}
	require.Equal(t, []string{"silences"}, got,
		"PollAwarePage with declared labels must only receive cached payloads for those labels (log=%v)", got)
}

// TestPollCache_EmptyPollResourcesGetsNothing covers the
// "filter active, allowed set empty" branch: a page that
// implements PollAwarePage but returns an empty slice opts out
// of cache replay entirely (matches the status page's vestigial
// DataMsg branch — see status.PollResources).
func TestPollCache_EmptyPollResourcesGetsNothing(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	a.Update(poll.DataMsg{Resource: []string{"a"}, Tenant: "prod", ResourceLabel: "alerts"})

	page := &filteringFakePage{fakePage: newFakePage("status"), labels: []string{}}
	drive(t, a, PushPage(func() Page { return page }))

	for _, msg := range *page.updateLog {
		_, isData := msg.(poll.DataMsg)
		require.False(t, isData,
			"PollAwarePage returning [] must receive no cached payloads (msg=%v)", msg)
	}
}

// TestPollCache_ReplacePageHydratesNewPage covers the symmetric
// path on `replacePage`: when a page is swapped (e.g. via the
// command bar's `:silences` after `:alerts`), the new page must
// hydrate from the cache the same way `pushPage` does.
func TestPollCache_ReplacePageHydratesNewPage(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	// Push an initial page so replacePage takes the swap branch
	// rather than falling back to push.
	first := newFakePage("first")
	drive(t, a, PushPage(func() Page { return first }))

	// Cache lands while `first` is on top.
	a.Update(poll.DataMsg{Resource: []string{"payload"}, Tenant: "prod", ResourceLabel: "alerts"})

	// Swap to a new page — the cache must replay into `second`,
	// not vanish with `first`.
	second := newFakePage("second")
	drive(t, a, ReplacePage(func() Page { return second }))

	var seen []string
	for _, msg := range *second.updateLog {
		if dm, ok := msg.(poll.DataMsg); ok {
			seen = append(seen, dm.Tenant)
		}
	}
	require.Equal(t, []string{"prod"}, seen,
		"replacePage must replay the cache into the new page (log=%v)", *second.updateLog)
}

// TestPollCache_OverwritesByResourceTenant guards the cache key:
// a second tick for the same (resource, tenant) replaces the
// stored payload — the page sees the freshest snapshot, not a
// history. Uses typed slices ([]string) for the payload so the
// fixture matches the production shape (real pages assert on
// []backend.Alert / []backend.Silence etc., not on bare strings).
func TestPollCache_OverwritesByResourceTenant(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	a.Update(poll.DataMsg{Resource: []string{"v1"}, Tenant: "prod", ResourceLabel: "alerts"})
	a.Update(poll.DataMsg{Resource: []string{"v2"}, Tenant: "prod", ResourceLabel: "alerts"})

	page := newFakePage("alerts")
	drive(t, a, PushPage(func() Page { return page }))

	var resources [][]string
	for _, msg := range *page.updateLog {
		if dm, ok := msg.(poll.DataMsg); ok {
			if r, ok := dm.Resource.([]string); ok {
				resources = append(resources, r)
			}
		}
	}
	require.Equal(t, [][]string{{"v2"}}, resources,
		"replay must surface the latest payload only — cache is a snapshot, not a log")
}

// TestPollCache_LegacyDataMsgIsForwardedNotCached covers tests
// (and pre-ResourceLabel callers) that emit DataMsg without the
// label. The App must still forward it to the active page so
// existing per-page tests keep working, but the cache stays out
// of the picture so we don't replay an "untyped" entry into the
// next page.
func TestPollCache_LegacyDataMsgIsForwardedNotCached(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	first := newFakePage("first")
	drive(t, a, PushPage(func() Page { return first }))

	a.Update(poll.DataMsg{Resource: "legacy", Tenant: "prod"}) // no ResourceLabel

	// First page received the live forward.
	require.Len(t, *first.updateLog, 1, "live DataMsg must reach the active page")

	// Now push a second page — it must NOT receive the legacy msg
	// from a replay, because we never cached it.
	second := newFakePage("second")
	drive(t, a, PushPage(func() Page { return second }))
	for _, msg := range *second.updateLog {
		_, isData := msg.(poll.DataMsg)
		require.False(t, isData,
			"unlabelled DataMsg must not be replayed into a freshly pushed page")
	}
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

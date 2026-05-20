// SPDX-License-Identifier: Apache-2.0

package pagetest_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
)

// stubPage is a minimal app.Page used to drive the harness in the
// pagetest unit tests. Production page packages can't be imported
// here (the harness must not depend on them); a hand-rolled stub
// keeps the test self-contained.
type stubPage struct {
	lastMsg  tea.Msg
	updates  int
	view     string
	replaces app.Page
	cmd      tea.Cmd
}

func (p *stubPage) Init() tea.Cmd { return nil }

func (p *stubPage) Update(msg tea.Msg) (app.Page, tea.Cmd) {
	p.lastMsg = msg
	p.updates++
	if p.replaces != nil {
		return p.replaces, p.cmd
	}
	return p, p.cmd
}

func (p *stubPage) Close() tea.Cmd            { return nil }
func (p *stubPage) View(_, _ int) string      { return p.view }
func (p *stubPage) Crumb() string             { return "stub" }
func (p *stubPage) Title() string             { return "stub" }
func (p *stubPage) HeaderContent() string     { return "" }
func (p *stubPage) Footer() string            { return "" }
func (p *stubPage) Bindings() []action.Action { return nil }

func TestHarness_UpdateRoutesIntoPageAndForwardsCmd(t *testing.T) {
	t.Parallel()

	page := &stubPage{cmd: func() tea.Msg { return "sentinel" }}
	h := pagetest.New(t, page)

	msg := tea.KeyPressMsg{Code: 'j', Text: "j"}
	cmd := h.Update(msg)
	require.Equal(t, 1, page.updates, "Update must delegate to the embedded page exactly once")
	require.Equal(t, msg, page.lastMsg, "Update must pass the message through verbatim")
	require.NotNil(t, cmd, "Update must surface the page's Cmd so callers can assert on it")
	require.Equal(t, "sentinel", cmd(), "the returned Cmd must be the page's Cmd, not a wrapper")
}

func TestHarness_UpdateTracksReturnedPage(t *testing.T) {
	t.Parallel()

	// app.Page.Update is allowed to return a different Page (some
	// pages re-assign on edit / drill transitions). The harness must
	// thread that replacement into subsequent View / Update calls
	// instead of silently keeping the original — otherwise tests
	// would assert against a stale page state.
	first := &stubPage{view: "first"}
	second := &stubPage{view: "second"}
	first.replaces = second

	h := pagetest.New(t, first)
	h.Update(tea.KeyPressMsg{Code: 'x'})
	require.Equal(t, "second", h.View(10, 10),
		"the harness must follow page replacements so subsequent View/Update see the new instance")
}

func TestHarness_ViewStripsStyles(t *testing.T) {
	t.Parallel()

	// The harness owns the strip step so migrated tests don't
	// re-import testutil; the assertion here is mechanical (no ANSI
	// in the output) rather than a specific colour, matching how
	// page tests use the strip path.
	page := &stubPage{view: "\x1b[31mred\x1b[0m plain"}
	h := pagetest.New(t, page)
	out := h.View(40, 5)
	require.Equal(t, "red plain", out,
		"View must return the stripped string so tests can do plain substring matches")
	require.NotContains(t, out, "\x1b",
		"stripped View output must not contain any ANSI escape")
}

func TestHarness_SendDispatchesAndDiscardsCmd(t *testing.T) {
	t.Parallel()

	// Send is the ergonomic shorthand for setup sequences where the
	// test doesn't care about the returned Cmd — the harness must
	// still drive Update (so state mutates) but the caller doesn't
	// have to write `_ = h.Update(...)` on every priming step.
	page := &stubPage{cmd: func() tea.Msg { return "ignored" }}
	h := pagetest.New(t, page)
	h.Send(tea.KeyPressMsg{Code: 'k'})
	require.Equal(t, 1, page.updates, "Send must still delegate to the page's Update")
}

func TestHarness_PageReturnsTrackedPointer(t *testing.T) {
	t.Parallel()

	// Page() exposes the currently-tracked app.Page so tests that
	// need to assert against concrete-type internals (e.g. a *Page
	// field for cursor index) can re-cast without juggling the
	// harness construction.
	page := &stubPage{}
	h := pagetest.New(t, page)
	require.Same(t, app.Page(page), h.Page(),
		"Page() must return the same pointer the harness is currently tracking")
}

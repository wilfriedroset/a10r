// SPDX-License-Identifier: Apache-2.0

package detailpage_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func TestBase_HandleSidebandMsg_GoToFirstRowResetsScroll(t *testing.T) {
	t.Parallel()

	b := &detailpage.Base{Scroll: 42}
	handled, cmd := b.HandleSidebandMsg(app.GoToFirstRowMsg{})

	require.True(t, handled, "GoToFirstRowMsg must be claimed")
	require.Nil(t, cmd, "GoToFirstRowMsg carries no follow-up Cmd")
	require.Equal(t, 0, b.Scroll, "Scroll must reset to 0")
}

func TestBase_HandleSidebandMsg_UnknownMessageFallsThrough(t *testing.T) {
	t.Parallel()
	type unrelatedMsg struct{}

	b := &detailpage.Base{
		SetTimeFormat: func(timerender.Format) {},
		OnModalResult: func(modal.ResultMsg) (bool, tea.Cmd) { return true, nil },
	}

	handled, cmd := b.HandleSidebandMsg(unrelatedMsg{})

	require.False(t, handled, "messages outside the sideband set must fall through")
	require.Nil(t, cmd)
}

func TestBase_HandleSidebandMsg_TimeFormatWired(t *testing.T) {
	t.Parallel()

	var got timerender.Format = -1
	b := &detailpage.Base{
		SetTimeFormat: func(f timerender.Format) { got = f },
	}

	handled, cmd := b.HandleSidebandMsg(app.TimeFormatChangedMsg{Format: timerender.Absolute})

	require.True(t, handled, "TimeFormatChangedMsg must be claimed when SetTimeFormat is wired")
	require.Nil(t, cmd)
	require.Equal(t, timerender.Absolute, got, "callback must receive the new format")
}

func TestBase_HandleSidebandMsg_TimeFormatUnwiredFallsThrough(t *testing.T) {
	t.Parallel()

	b := &detailpage.Base{}
	handled, cmd := b.HandleSidebandMsg(app.TimeFormatChangedMsg{Format: timerender.Absolute})

	require.False(t, handled, "TimeFormatChangedMsg without SetTimeFormat must fall through")
	require.Nil(t, cmd)
}

// fakeModalResult is a stand-in modal.ResultMsg for the sideband
// dispatch test — concrete results (e.g. ConfirmResultMsg) all
// satisfy modal.ResultMsg via a single marker method.
type fakeModalResult struct{}

func (fakeModalResult) IsModalResult() {}

func TestBase_HandleSidebandMsg_ModalResultWired(t *testing.T) {
	t.Parallel()

	sentinel := tea.Msg("modal-handled")
	var got modal.ResultMsg
	b := &detailpage.Base{
		OnModalResult: func(m modal.ResultMsg) (bool, tea.Cmd) {
			got = m
			return true, func() tea.Msg { return sentinel }
		},
	}

	handled, cmd := b.HandleSidebandMsg(fakeModalResult{})

	require.True(t, handled, "ModalResultMsg must be claimed when OnModalResult is wired")
	require.NotNil(t, cmd, "the wired Cmd must propagate")
	require.Equal(t, sentinel, cmd())
	require.NotNil(t, got, "the result message must reach the callback")
}

func TestBase_HandleSidebandMsg_ModalResultUnwiredFallsThrough(t *testing.T) {
	t.Parallel()

	b := &detailpage.Base{}
	handled, cmd := b.HandleSidebandMsg(fakeModalResult{})

	require.False(t, handled, "ModalResultMsg without OnModalResult must fall through")
	require.Nil(t, cmd)
}

func TestBase_HandleSidebandMsg_ModalResultCallbackCanDecline(t *testing.T) {
	t.Parallel()

	// A page may receive a ResultMsg whose concrete type it doesn't
	// recognise (a sibling modal's result reaching the wrong page,
	// say). The callback can return handled=false to let the page's
	// main switch ignore the message — proves the dispatch is honest
	// about delegation rather than rubber-stamping every ResultMsg.
	b := &detailpage.Base{
		OnModalResult: func(modal.ResultMsg) (bool, tea.Cmd) { return false, nil },
	}

	handled, cmd := b.HandleSidebandMsg(fakeModalResult{})

	require.False(t, handled, "the callback's handled=false must propagate")
	require.Nil(t, cmd)
}

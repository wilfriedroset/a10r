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

// fakeModalResult is a stand-in modal.ResultMsg for the sideband
// dispatch test — concrete results (e.g. ConfirmResultMsg) all
// satisfy modal.ResultMsg via a single marker method.
type fakeModalResult struct{}

func (fakeModalResult) IsModalResult() {}

func TestBase_HandleSidebandMsg(t *testing.T) {
	t.Parallel()

	type unrelatedMsg struct{}

	sentinel := tea.Msg("modal-handled")

	cases := []struct {
		name        string
		baseFactory func(t *testing.T) (*detailpage.Base, func(t *testing.T))
		msg         tea.Msg
		wantHandled bool
		wantCmdMsg  tea.Msg
	}{
		{
			name: "goto first row resets scroll",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				b := &detailpage.Base{Scroll: 42}
				return b, func(t *testing.T) {
					t.Helper()
					require.Equal(t, 0, b.Scroll, "Scroll must reset to 0")
				}
			},
			msg:         app.GoToFirstRowMsg{},
			wantHandled: true,
		},
		{
			// Anything outside the sideband set must fall through so
			// the page's main switch can claim it (or not). Optionals
			// are wired so the test proves a fall-through even when
			// the router has every hook available.
			name: "unknown message falls through",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				b := &detailpage.Base{
					SetTimeFormat: func(timerender.Format) {},
					OnModalResult: func(modal.ResultMsg) (bool, tea.Cmd) { return true, nil },
				}
				return b, nil
			},
			msg: unrelatedMsg{},
		},
		{
			name: "time format wired",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				var got timerender.Format = -1
				b := &detailpage.Base{
					SetTimeFormat: func(f timerender.Format) { got = f },
				}
				return b, func(t *testing.T) {
					t.Helper()
					require.Equal(t, timerender.Absolute, got, "callback must receive the new format")
				}
			},
			msg:         app.TimeFormatChangedMsg{Format: timerender.Absolute},
			wantHandled: true,
		},
		{
			// Pages without time-formatted fields don't wire
			// SetTimeFormat — the message must fall through cleanly.
			name: "time format unwired falls through",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				return &detailpage.Base{}, nil
			},
			msg: app.TimeFormatChangedMsg{Format: timerender.Absolute},
		},
		{
			name: "modal result wired",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				var got modal.ResultMsg
				b := &detailpage.Base{
					OnModalResult: func(m modal.ResultMsg) (bool, tea.Cmd) {
						got = m
						return true, func() tea.Msg { return sentinel }
					},
				}
				return b, func(t *testing.T) {
					t.Helper()
					require.NotNil(t, got, "the result message must reach the callback")
				}
			},
			msg:         fakeModalResult{},
			wantHandled: true,
			wantCmdMsg:  sentinel,
		},
		{
			name: "modal result unwired falls through",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				return &detailpage.Base{}, nil
			},
			msg: fakeModalResult{},
		},
		{
			// A page may receive a ResultMsg whose concrete type it doesn't
			// recognise (a sibling modal's result reaching the wrong page,
			// say). The callback can return handled=false to let the page's
			// main switch ignore the message — proves the dispatch is honest
			// about delegation rather than rubber-stamping every ResultMsg.
			name: "modal result callback can decline",
			baseFactory: func(t *testing.T) (*detailpage.Base, func(t *testing.T)) {
				t.Helper()
				b := &detailpage.Base{
					OnModalResult: func(modal.ResultMsg) (bool, tea.Cmd) { return false, nil },
				}
				return b, nil
			},
			msg: fakeModalResult{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, verify := tc.baseFactory(t)
			handled, cmd := base.HandleSidebandMsg(tc.msg)
			require.Equal(t, tc.wantHandled, handled, "handled disposition for %s", tc.name)
			if tc.wantCmdMsg == nil {
				require.Nil(t, cmd, "%s must not return a follow-up Cmd", tc.name)
			} else {
				require.NotNil(t, cmd, "%s must propagate the wired Cmd", tc.name)
				require.Equal(t, tc.wantCmdMsg, cmd())
			}
			if verify != nil {
				verify(t)
			}
		})
	}
}

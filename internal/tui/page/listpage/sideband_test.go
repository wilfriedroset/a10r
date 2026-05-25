// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

func TestBase_HandleSidebandMsg(t *testing.T) {
	t.Parallel()

	type unrelatedMsg struct{}

	sentinel := tea.Msg("marks-cleared")

	cases := []struct {
		name        string
		baseFactory func(t *testing.T) (*listpage.Base, func(t *testing.T))
		msg         tea.Msg
		wantPanic   string
		wantHandled bool
		wantCmdMsg  tea.Msg
	}{
		{
			name: "scope change",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				calls := 0
				b := &listpage.Base{
					Scope:     "all",
					Recompute: func() { calls++ },
				}
				return b, func(t *testing.T) {
					t.Helper()
					require.Equal(t, "prod", b.Scope)
					require.Equal(t, 1, calls, "recompute must fire exactly once")
				}
			},
			msg:         app.ScopeChangedMsg{Scope: "prod"},
			wantHandled: true,
		},
		{
			// Anything outside the sideband set must fall through so
			// the page's main switch can claim it (or not).
			name: "unknown message falls through",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				b := &listpage.Base{
					Recompute:     func() {},
					RowCount:      func() int { return 0 },
					SnapshotFocus: func() {},
					SetTimeFormat: func(timerender.Format) {},
					ClearMarks:    func() tea.Cmd { return nil },
				}
				return b, nil
			},
			msg: unrelatedMsg{},
		},
		{
			name: "clear marks wired",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				cmdFn := func() tea.Msg { return sentinel }
				b := &listpage.Base{
					ClearMarks: func() tea.Cmd { return cmdFn },
				}
				return b, nil
			},
			msg:         app.ClearMarksMsg{},
			wantHandled: true,
			wantCmdMsg:  sentinel,
		},
		{
			// A page without ClearMarks wired (groups, receivers) must
			// not claim the message — it has no marks to clear.
			name: "clear marks unwired falls through",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				return &listpage.Base{}, nil
			},
			msg: app.ClearMarksMsg{},
		},
		{
			name: "time format wired",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				var got timerender.Format = -1
				b := &listpage.Base{
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
			// Pages without time columns (groups, receivers) don't wire
			// SetTimeFormat — the message must fall through cleanly.
			name: "time format unwired falls through",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				return &listpage.Base{}, nil
			},
			msg: app.TimeFormatChangedMsg{Format: timerender.Absolute},
		},
		{
			// GoToFirstRow needs both callbacks; missing either is a
			// page-constructor bug, not a runtime fall-through.
			name: "goto first row panics without RowCount",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				return &listpage.Base{SnapshotFocus: func() {}}, nil
			},
			msg:       app.GoToFirstRowMsg{},
			wantPanic: "listpage.Base.HandleSidebandMsg: RowCount callback not wired by page constructor",
		},
		{
			name: "goto first row panics without SnapshotFocus",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				return &listpage.Base{RowCount: func() int { return 0 }}, nil
			},
			msg:       app.GoToFirstRowMsg{},
			wantPanic: "listpage.Base.HandleSidebandMsg: SnapshotFocus callback not wired by page constructor",
		},
		{
			name: "goto first row",
			baseFactory: func(t *testing.T) (*listpage.Base, func(t *testing.T)) {
				t.Helper()
				snapshots := 0
				b := &listpage.Base{
					Recompute:     func() {},
					RowCount:      func() int { return 42 },
					SnapshotFocus: func() { snapshots++ },
				}
				b.SetIndex(7, 42)
				return b, func(t *testing.T) {
					t.Helper()
					require.Equal(t, 0, b.Index(), "cursor must land on row 0")
					require.Equal(t, 1, snapshots, "snapshotFocus must fire exactly once")
				}
			},
			msg:         app.GoToFirstRowMsg{},
			wantHandled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, verify := tc.baseFactory(t)
			if tc.wantPanic != "" {
				require.PanicsWithValue(t, tc.wantPanic, func() {
					base.HandleSidebandMsg(tc.msg)
				})
				return
			}
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

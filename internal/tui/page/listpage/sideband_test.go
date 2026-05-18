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

func TestBase_HandleSidebandMsg_ScopeChange(t *testing.T) {
	t.Parallel()

	calls := 0
	b := &listpage.Base{
		Scope:     "all",
		Recompute: func() { calls++ },
	}

	handled, cmd := b.HandleSidebandMsg(app.ScopeChangedMsg{Scope: "prod"})

	require.True(t, handled, "ScopeChangedMsg must be claimed by the sideband router")
	require.Nil(t, cmd, "ScopeChangedMsg has no follow-up Cmd")
	require.Equal(t, "prod", b.Scope)
	require.Equal(t, 1, calls, "recompute must fire exactly once")
}

func TestBase_HandleSidebandMsg_UnknownMessageFallsThrough(t *testing.T) {
	t.Parallel()

	type unrelatedMsg struct{}

	b := &listpage.Base{
		Recompute:     func() {},
		RowCount:      func() int { return 0 },
		SnapshotFocus: func() {},
		SetTimeFormat: func(timerender.Format) {},
		ClearMarks:    func() tea.Cmd { return nil },
	}

	handled, cmd := b.HandleSidebandMsg(unrelatedMsg{})

	require.False(t, handled, "messages outside the sideband set must fall through")
	require.Nil(t, cmd)
}

func TestBase_HandleSidebandMsg_ClearMarksWired(t *testing.T) {
	t.Parallel()

	sentinel := tea.Msg("marks-cleared")
	cmdFn := func() tea.Msg { return sentinel }
	b := &listpage.Base{
		ClearMarks: func() tea.Cmd { return cmdFn },
	}

	handled, cmd := b.HandleSidebandMsg(app.ClearMarksMsg{})

	require.True(t, handled, "ClearMarksMsg must be claimed when ClearMarks is wired")
	require.NotNil(t, cmd, "ClearMarks cmd must propagate to the caller")
	require.Equal(t, sentinel, cmd())
}

func TestBase_HandleSidebandMsg_ClearMarksUnwiredFallsThrough(t *testing.T) {
	t.Parallel()

	b := &listpage.Base{}

	handled, cmd := b.HandleSidebandMsg(app.ClearMarksMsg{})

	require.False(t, handled, "ClearMarksMsg without ClearMarks must fall through")
	require.Nil(t, cmd)
}

func TestBase_HandleSidebandMsg_TimeFormatWired(t *testing.T) {
	t.Parallel()

	var got timerender.Format = -1
	b := &listpage.Base{
		SetTimeFormat: func(f timerender.Format) { got = f },
	}

	handled, cmd := b.HandleSidebandMsg(app.TimeFormatChangedMsg{Format: timerender.Absolute})

	require.True(t, handled, "TimeFormatChangedMsg must be claimed when SetTimeFormat is wired")
	require.Nil(t, cmd)
	require.Equal(t, timerender.Absolute, got, "callback must receive the new format")
}

func TestBase_HandleSidebandMsg_TimeFormatUnwiredFallsThrough(t *testing.T) {
	t.Parallel()

	b := &listpage.Base{}

	handled, cmd := b.HandleSidebandMsg(app.TimeFormatChangedMsg{Format: timerender.Absolute})

	require.False(t, handled, "TimeFormatChangedMsg without SetTimeFormat must fall through")
	require.Nil(t, cmd)
}

func TestBase_HandleSidebandMsg_GoToFirstRowPanicsWithoutCallbacks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		base *listpage.Base
		want string
	}{
		{
			name: "RowCount nil",
			base: &listpage.Base{
				SnapshotFocus: func() {},
			},
			want: "listpage.Base.HandleSidebandMsg: RowCount callback not wired by page constructor",
		},
		{
			name: "SnapshotFocus nil",
			base: &listpage.Base{
				RowCount: func() int { return 0 },
			},
			want: "listpage.Base.HandleSidebandMsg: SnapshotFocus callback not wired by page constructor",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.PanicsWithValue(t, tc.want, func() {
				tc.base.HandleSidebandMsg(app.GoToFirstRowMsg{})
			})
		})
	}
}

func TestBase_HandleSidebandMsg_GoToFirstRow(t *testing.T) {
	t.Parallel()

	snapshots := 0
	b := &listpage.Base{
		Recompute:     func() {},
		RowCount:      func() int { return 42 },
		SnapshotFocus: func() { snapshots++ },
	}
	b.SetIndex(7, 42)

	handled, cmd := b.HandleSidebandMsg(app.GoToFirstRowMsg{})

	require.True(t, handled, "GoToFirstRowMsg must be claimed by the sideband router")
	require.Nil(t, cmd, "GoToFirstRowMsg has no follow-up Cmd")
	require.Equal(t, 0, b.Index(), "cursor must land on row 0")
	require.Equal(t, 1, snapshots, "snapshotFocus must fire exactly once")
}

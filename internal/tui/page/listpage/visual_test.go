// SPDX-License-Identifier: Apache-2.0

package listpage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

type visualRow struct{ key string }

func visualKey(r visualRow) string { return r.key }

func visualView(keys ...string) []visualRow {
	out := make([]visualRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, visualRow{key: k})
	}
	return out
}

// visualBase returns a Base whose cursor sits on cursorIdx, with
// visual mode anchored on anchor when anchor is non-empty.
func visualBase(anchor string, cursorIdx int) *listpage.Base {
	b := &listpage.Base{Window: cursor.NewWindow(cursorIdx, 0, 10)}
	b.Visual.Start(anchor)
	return b
}

func marksOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestVisual_StartRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	var v listpage.Visual
	v.Start("")
	require.False(t, v.On(), "an empty anchor could never be resolved back to a row")
}

func TestVisual_CancelClearsAnchor(t *testing.T) {
	t.Parallel()
	b := visualBase("a", 0)
	b.Visual.Cancel()
	require.False(t, b.Visual.On())
	require.False(t, listpage.VisualPreview(b, visualView("a", "b"), visualKey).Open(),
		"a cancelled visual must not resolve its old anchor")
}

func TestVisualPreview(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		anchor    string
		view      []visualRow
		cursorIdx int
		wantLo    int
		wantHi    int
		wantOpen  bool
	}{
		{name: "visual off", view: visualView("a", "b", "c"), cursorIdx: 1},
		{
			name: "anchor above cursor", anchor: "a", view: visualView("a", "b", "c", "d"),
			cursorIdx: 2, wantLo: 0, wantHi: 2, wantOpen: true,
		},
		{
			name: "anchor below cursor", anchor: "d", view: visualView("a", "b", "c", "d"),
			cursorIdx: 1, wantLo: 1, wantHi: 3, wantOpen: true,
		},
		{
			name: "anchor equals cursor", anchor: "b", view: visualView("a", "b", "c"),
			cursorIdx: 1, wantLo: 1, wantHi: 1, wantOpen: true,
		},
		{name: "anchor row gone", anchor: "z", view: visualView("a", "b"), cursorIdx: 0},
		{name: "cursor past the view", anchor: "a", view: visualView("a", "b"), cursorIdx: 7},
		{name: "empty view", anchor: "a", view: nil, cursorIdx: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := listpage.VisualPreview(visualBase(tc.anchor, tc.cursorIdx), tc.view, visualKey)
			require.Equal(t, tc.wantOpen, r.Open())
			if !r.Open() {
				require.False(t, r.Covers(0), "a closed range covers no row, not even row zero")
				return
			}
			require.False(t, r.Covers(tc.wantLo-1), "the row above the range stays out")
			require.True(t, r.Covers(tc.wantLo))
			require.True(t, r.Covers(tc.wantHi))
			require.False(t, r.Covers(tc.wantHi+1), "the row below the range stays out")
		})
	}
}

func TestCommitVisual(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		anchor    string
		cursorIdx int
		seed      []string
		wantMarks []string
		wantOpen  bool
	}{
		{
			name: "commits the whole span", anchor: "a", cursorIdx: 2,
			wantMarks: []string{"a", "b", "c"}, wantOpen: true,
		},
		{
			name: "keeps marks outside the span", anchor: "a", cursorIdx: 1,
			seed: []string{"d"}, wantMarks: []string{"a", "b", "d"}, wantOpen: true,
		},
		{
			name: "an already marked row stays marked", anchor: "a", cursorIdx: 1,
			seed: []string{"b"}, wantMarks: []string{"a", "b"}, wantOpen: true,
		},
		{name: "visual off marks nothing", cursorIdx: 2},
		{name: "a lost anchor marks nothing", anchor: "z", cursorIdx: 2, wantOpen: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			marks := map[string]struct{}{}
			for _, k := range tc.seed {
				marks[k] = struct{}{}
			}
			b := visualBase(tc.anchor, tc.cursorIdx)
			view := visualView("a", "b", "c", "d")
			require.Equal(t, tc.wantOpen, listpage.CommitVisual(b, view, marks, visualKey))
			require.ElementsMatch(t, tc.wantMarks, marksOf(marks))
			require.False(t, b.Visual.On(), "a commit always leaves visual mode")
		})
	}
}

func TestMarkOrCommit_TogglesWhenNoRangeIsOpen(t *testing.T) {
	t.Parallel()
	b := visualBase("", 1)
	view := visualView("a", "b", "c")
	marks := map[string]struct{}{}

	listpage.MarkOrCommit(b, view, marks, visualKey)
	require.ElementsMatch(t, []string{"b"}, marksOf(marks))

	listpage.MarkOrCommit(b, view, marks, visualKey)
	require.Empty(t, marksOf(marks), "a second Space untoggles the cursor row")
}

func TestMarkOrCommit_CommitsAnOpenRange(t *testing.T) {
	t.Parallel()
	b := visualBase("a", 2)
	marks := map[string]struct{}{}
	listpage.MarkOrCommit(b, visualView("a", "b", "c"), marks, visualKey)
	require.ElementsMatch(t, []string{"a", "b", "c"}, marksOf(marks))
}

func TestStartOrCommitVisual(t *testing.T) {
	t.Parallel()
	b := &listpage.Base{Window: cursor.NewWindow(1, 0, 10)}
	view := visualView("a", "b", "c")
	marks := map[string]struct{}{}

	listpage.StartOrCommitVisual(b, view, marks, visualKey)
	require.True(t, b.Visual.On(), "the first V anchors on the cursor row")
	require.Empty(t, marksOf(marks), "anchoring marks nothing yet")

	b.SetIndex(2, len(view))
	listpage.StartOrCommitVisual(b, view, marks, visualKey)
	require.False(t, b.Visual.On())
	require.ElementsMatch(t, []string{"b", "c"}, marksOf(marks), "the second V commits the span")
}

func TestStartOrCommitVisual_EmptyViewDoesNotAnchor(t *testing.T) {
	t.Parallel()
	b := &listpage.Base{}
	listpage.StartOrCommitVisual(b, []visualRow(nil), map[string]struct{}{}, visualKey)
	require.False(t, b.Visual.On(), "there is no row to anchor on")
}

func TestCancelVisualOnLostAnchor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		anchor     string
		view       []visualRow
		wantCancel bool
	}{
		{name: "anchor still present", anchor: "b", view: visualView("a", "b"), wantCancel: false},
		{name: "anchor filtered out", anchor: "b", view: visualView("a", "c"), wantCancel: true},
		{name: "view emptied", anchor: "b", view: nil, wantCancel: true},
		{name: "visual off", view: nil, wantCancel: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := visualBase(tc.anchor, 0)
			cmd := listpage.CancelVisualOnLostAnchor(b, tc.view, visualKey)
			if !tc.wantCancel {
				require.Nil(t, cmd)
				require.Equal(t, tc.anchor != "", b.Visual.On())
				return
			}
			require.False(t, b.Visual.On())
			require.NotNil(t, cmd, "a cancelled range must state its reason")
		})
	}
}

func TestBase_ConsumeEscape(t *testing.T) {
	t.Parallel()
	b := &listpage.Base{}
	require.False(t, b.ConsumeEscape(), "with no range open Esc belongs to the global pop")

	b.Visual.Start("a")
	require.True(t, b.ConsumeEscape(), "Esc unwinds the range before it pops the page")
	require.False(t, b.Visual.On())
	require.False(t, b.ConsumeEscape())
}

func TestBase_SuspendCancelsVisual(t *testing.T) {
	t.Parallel()
	b := visualBase("row-1", 0)
	b.Suspend()
	require.False(t, b.Visual.On(),
		"a drill-down covers the page, and the range must not be waiting when it comes back")
}

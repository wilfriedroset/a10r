// SPDX-License-Identifier: Apache-2.0

package clipboard

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	require.NotNil(t, Resolve(nil), "nil injection must fall back to the OSC52 default")
	f := &testutil.FakeClipboard{}
	require.Equal(t, Clipboard(f), Resolve(f), "an injected clipboard must be returned untouched")
}

func TestItems(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", pickerValueWidth+20)
	tests := []struct {
		name   string
		fields []Field
		want   []string
	}{
		{
			name:   "name and value joined",
			fields: []Field{{Name: "label severity", Value: "critical"}},
			want:   []string{"label severity: critical"},
		},
		{
			name:   "empty value is listed",
			fields: []Field{{Name: "annotation runbook", Value: ""}},
			want:   []string{"annotation runbook: "},
		},
		{
			name:   "newlines flatten to one row",
			fields: []Field{{Name: "annotation summary", Value: "first\nsecond"}},
			want:   []string{"annotation summary: first second"},
		},
		{
			name:   "long value is cut for display",
			fields: []Field{{Name: "k", Value: long}},
			want:   []string{"k: " + strings.Repeat("x", pickerValueWidth-1) + "…"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, Items(tc.fields))
		})
	}
}

func TestCopy_SendsFullValueNotTheRenderedRow(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", pickerValueWidth+20)
	fields := []Field{{Name: "a", Value: "one"}, {Name: "k", Value: long}}
	f := &testutil.FakeClipboard{}
	cmd := CopySelected(f, fields, modal.PickerSubmittedMsg{Origin: PickerOrigin, Indexes: []int{1}})
	require.NotNil(t, cmd)
	msg := flashFrom(t, cmd)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Equal(t, "copied k", msg.Text)
	require.Equal(t, 1, f.Calls)
	require.Equal(t, long, f.Last, "the copied text must be the full value, never the cut row")
}

func TestCopy_IgnoresOutOfRangeSelection(t *testing.T) {
	t.Parallel()

	fields := []Field{{Name: "a", Value: "one"}}
	f := &testutil.FakeClipboard{}
	for _, idx := range [][]int{nil, {}, {1}, {-1}} {
		require.Nil(t, CopySelected(f, fields, modal.PickerSubmittedMsg{Indexes: idx}),
			"an index outside the field slice must be a no-op, not a panic")
	}
	require.Zero(t, f.Calls)
}

// flashFrom runs cmd and returns the FlashShowMsg it produces,
// unwrapping the tea.Batch that pairs the copy with its flash.
func flashFrom(t *testing.T, cmd tea.Cmd) footer.FlashShowMsg {
	t.Helper()
	require.NotNil(t, cmd)
	switch m := cmd().(type) {
	case footer.FlashShowMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if fm, ok := c().(footer.FlashShowMsg); ok {
				return fm
			}
		}
	}
	t.Fatalf("cmd produced no FlashShowMsg")
	return footer.FlashShowMsg{}
}

func TestPickerSubmitIndexesMatchTheFieldSlice(t *testing.T) {
	t.Parallel()

	// Items and Copy are called on separate frames, so the picker's
	// index must still address the same field on the way back. This
	// walks the real modal rather than a hand-built message.
	long := strings.Repeat("z", pickerValueWidth+20)
	fields := []Field{
		{Name: "id", Value: "sil-1"},
		{Name: "comment", Value: long},
		{Name: "state", Value: "active"},
	}
	p := newPicker(fields)
	// Filter first: on the unfiltered list the picker's match index
	// equals the item index, so only a narrowed list can catch a
	// matches-versus-items mix-up on the way back.
	for _, r := range "comm" {
		_, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	submitted, ok := cmd().(modal.PickerSubmittedMsg)
	require.True(t, ok)

	f := &testutil.FakeClipboard{}
	msg := flashFrom(t, CopySelected(f, fields, submitted))
	require.Equal(t, "copied comment", msg.Text,
		"the query leaves one row at match index 0, whose item index is 1")
	require.Equal(t, long, f.Last)
}

func TestPickerSearchesPastTheDisplayCut(t *testing.T) {
	t.Parallel()

	fields := []Field{
		{Name: "id", Value: "sil-1"},
		{Name: "comment", Value: strings.Repeat("x", pickerValueWidth+20) + " ticket-42"},
	}
	p := newPicker(fields)
	for _, r := range "ticket-42" {
		_, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	submitted, ok := cmd().(modal.PickerSubmittedMsg)
	require.True(t, ok)
	require.Equal(t, []int{1}, submitted.Indexes)
}

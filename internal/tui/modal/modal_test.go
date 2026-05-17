// SPDX-License-Identifier: Apache-2.0

package modal

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// ----- picker -----

func TestPicker_SingleSelectEnterReturnsCursor(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod", "staging", "dev"}, PickerSingle)
	// Move cursor to row 1 ("staging").
	p, _ = updateAs(p, tea.KeyPressMsg{Code: tea.KeyDown})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(PickerSubmittedMsg)
	require.Equal(t, []string{"staging"}, msg.Selections)
}

func TestPicker_MultiSelectSpaceAndEnter(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod", "staging", "dev"}, PickerMulti)
	// Mark prod (row 0) and dev (row 2). Space toggles.
	p, _ = updateAs(p, tea.KeyPressMsg{Code: ' ', Text: " "})
	p, _ = updateAs(p, tea.KeyPressMsg{Code: tea.KeyDown})
	p, _ = updateAs(p, tea.KeyPressMsg{Code: tea.KeyDown})
	p, _ = updateAs(p, tea.KeyPressMsg{Code: ' ', Text: " "})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(PickerSubmittedMsg)
	require.Equal(t, []string{"prod", "dev"}, msg.Selections,
		"selections must come back in original-input order")
}

func TestPicker_MultiSelectAllOnEmptyQuery(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod", "staging", "dev"}, PickerMulti)
	p, _ = updateAs(p, tea.KeyPressMsg{Code: 'a', Text: "a"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(PickerSubmittedMsg)
	require.Equal(t, []string{"prod", "staging", "dev"}, msg.Selections)
}

func TestPicker_QueryFiltersFuzzy(t *testing.T) {
	t.Parallel()

	p := NewPicker("backends", []string{"alertmanager", "mimir", "thanos"}, PickerSingle)
	for _, r := range "mim" {
		p, _ = updateAs(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "mim", p.query)

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(PickerSubmittedMsg)
	require.Equal(t, []string{"mimir"}, msg.Selections,
		"fuzzy match must put the best-fit row at the cursor")
}

func TestPicker_AKeyTypedIntoQueryAfterAlreadyFiltering(t *testing.T) {
	t.Parallel()

	// `a` selects-all only when the query is empty. Once the user
	// has typed, `a` is just an `a` rune appended to the buffer.
	p := NewPicker("backends", []string{"alpha", "bravo"}, PickerMulti)
	p, _ = updateAs(p, tea.KeyPressMsg{Code: 'b', Text: "b"})
	p, _ = updateAs(p, tea.KeyPressMsg{Code: 'a', Text: "a"})

	require.Equal(t, "ba", p.query,
		"`a` after a typed query must extend the query, not select-all")
	require.Empty(t, p.marks,
		"the `a` keypress must NOT have triggered select-all when the query is non-empty")
}

func TestPicker_EscEmitsCancelled(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod"}, PickerSingle)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, ok := cmd().(PickerCancelledMsg)
	require.True(t, ok)
}

func TestPicker_CtrlUClearsQuery(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod", "staging"}, PickerSingle)
	for _, r := range "stag" {
		p, _ = updateAs(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Equal(t, "stag", p.query)
	p, _ = updateAs(p, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, p.query)
}

func TestPicker_BackspaceTrimsQuery(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod"}, PickerSingle)
	for _, r := range "stag" {
		p, _ = updateAs(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p, _ = updateAs(p, tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "sta", p.query)
}

func TestPicker_CursorClampsOnEmptyMatches(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod", "staging"}, PickerSingle)
	for _, r := range "zzz" {
		p, _ = updateAs(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	require.Empty(t, p.matches)
	require.Equal(t, 0, p.cursor)

	// Submit on empty matches must NOT panic and must emit Cancelled.
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, ok := cmd().(PickerCancelledMsg)
	require.True(t, ok, "submit on empty match list must cancel, not crash")
}

func TestPicker_CursorBoundedByMatches(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"a", "b"}, PickerSingle)
	// Walk past the bottom — cursor must clamp at len-1 = 1.
	for range 5 {
		p, _ = updateAs(p, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	require.Equal(t, 1, p.cursor)
	// Walk past the top — cursor must clamp at 0.
	for range 5 {
		p, _ = updateAs(p, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	require.Equal(t, 0, p.cursor)
}

func TestPicker_ViewIncludesItems(t *testing.T) {
	t.Parallel()

	p := NewPicker("tenants", []string{"prod", "staging"}, PickerSingle)
	require.Equal(t, "tenants", p.Title(),
		"the title is owned by Modal.Title and rendered by the App's "+
			"outer panel border — not the picker body")
	out := testutil.StripStyle(p.View(40, 20))
	require.NotContains(t, out, "tenants",
		"the body must NOT print the title or it would duplicate "+
			"the panel border label")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "staging")
}

// ----- confirm -----

func TestConfirm_YResolvesYes(t *testing.T) {
	t.Parallel()
	c := NewConfirm("expire silence?", ConfirmDefaultNo)
	_, cmd := c.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	msg := cmd().(ConfirmResultMsg)
	require.True(t, msg.Yes)
	require.False(t, msg.Cancelled)
}

func TestConfirm_NResolvesNo(t *testing.T) {
	t.Parallel()
	c := NewConfirm("expire silence?", ConfirmDefaultYes)
	_, cmd := c.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	msg := cmd().(ConfirmResultMsg)
	require.False(t, msg.Yes)
	require.False(t, msg.Cancelled)
}

func TestConfirm_EnterUsesDefault(t *testing.T) {
	t.Parallel()
	c := NewConfirm("apply?", ConfirmDefaultNo)
	_, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(ConfirmResultMsg)
	require.False(t, msg.Yes,
		"Enter on default-No must resolve as not-yes — destructive flows must not yes-by-default")

	c2 := NewConfirm("apply?", ConfirmDefaultYes)
	_, cmd = c2.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg = cmd().(ConfirmResultMsg)
	require.True(t, msg.Yes)
}

func TestConfirm_EscCancels(t *testing.T) {
	t.Parallel()
	c := NewConfirm("apply?", ConfirmDefaultNo)
	_, cmd := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	msg := cmd().(ConfirmResultMsg)
	require.True(t, msg.Cancelled)
}

func TestConfirm_StrayKeyIgnored(t *testing.T) {
	t.Parallel()
	c := NewConfirm("apply?", ConfirmDefaultNo)
	_, cmd := c.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.Nil(t, cmd, "unrecognised keys must be silent — no accidental pick")
}

// updateAs runs a Picker.Update and asserts the returned Modal is
// the same Picker. Tests use it instead of bare Update so the
// picker-specific fields stay accessible.
func updateAs(p *Picker, msg tea.Msg) (*Picker, tea.Cmd) {
	m, cmd := p.Update(msg)
	pp, ok := m.(*Picker)
	if !ok {
		panic("Picker.Update must return *Picker")
	}
	return pp, cmd
}

// SPDX-License-Identifier: Apache-2.0

package tenant

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

func sampleRows() []Row {
	return []Row{
		{Name: "prod", Conn: header.ConnConnected, Alerts: 12},
		{Name: "staging", Conn: header.ConnDegraded, Alerts: 0},
		{Name: "dev", Conn: header.ConnUnreachable, Alerts: 0},
	}
}

func TestPage_SetRowsClampsCursor(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	p.cursor = 99
	p.SetRows(sampleRows())
	require.Less(t, p.cursor, len(sampleRows()))
}

func TestPage_SpaceTogglesMark(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	// Cursor at 0; rows are sorted alphabetically — first row is dev.
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Contains(t, p.marks, "prod",
		"the cursor row at index 0 maps to the unsorted first row in p.rows; mark must reflect that")
}

func TestPage_AKeySelectsAll(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.Len(t, p.marks, len(sampleRows()))
}

func TestPage_EnterSubmitsMarks(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // mark prod (index 0)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(SelectedMsg)
	require.Equal(t, []string{"prod"}, msg.Selections)
}

func TestPage_EnterWithoutMarksFallsBackToCursor(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(SelectedMsg)
	require.Equal(t, []string{"staging"}, msg.Selections,
		"Enter with no marks falls back to the single cursor row")
}

func TestPage_ZeroSubmitsAll(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	_, cmd := p.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	msg := cmd().(SelectedMsg)
	require.ElementsMatch(t, []string{"prod", "staging", "dev"}, msg.Selections)
}

func TestPage_NumericQuickSwitch(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows()) // unsorted: prod, staging, dev

	_, cmd := p.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	msg := cmd().(SelectedMsg)
	require.Equal(t, []string{"staging"}, msg.Selections,
		"`2` selects the 2nd configured backend in original order")
}

func TestPage_NumericBeyondCountIsNoOp(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows()[:1]) // only one backend

	_, cmd := p.Update(tea.KeyPressMsg{Code: '5', Text: "5"})
	require.Nil(t, cmd, "`5` with one backend must be a no-op, not a panic")
}

func TestPage_RenderShowsEveryRow(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	out := p.View(80, 10)
	for _, name := range []string{"prod", "staging", "dev"} {
		require.Contains(t, out, name)
	}
}

func TestPage_HeaderShowsSelectionCount(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	require.Contains(t, p.HeaderContent(), "0 selected")
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Contains(t, p.HeaderContent(), "1 selected")
}

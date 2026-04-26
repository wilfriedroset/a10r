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

func TestPage_DigitsAreNotPageOwned(t *testing.T) {
	t.Parallel()
	// `0`, `1`-`9` are owned by the App's LayerGlobal binding so
	// every page reacts the same way (ScopeChangedMsg). The
	// dispatcher consumes them before forwardToTop runs, so the
	// tenant page must NOT bind them locally — otherwise we'd be
	// chasing two competing handlers per digit.
	p := New(loadStyles(t))
	p.SetRows(sampleRows())
	for _, code := range []rune{'0', '1', '2', '5', '9'} {
		_, cmd := p.Update(tea.KeyPressMsg{Code: code, Text: string(code)})
		require.Nilf(t, cmd, "digit %q must NOT be locally handled", string(code))
	}
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
	require.Empty(t, p.HeaderContent(),
		"with no marks the header is silent — count lives in the title")
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Contains(t, p.HeaderContent(), "1 selected")
}

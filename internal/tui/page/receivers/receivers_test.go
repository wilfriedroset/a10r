// SPDX-License-Identifier: Apache-2.0

package receivers

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

func TestPage_DataMsgSortsReceivers(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "web"}, {Name: "ops"}, {Name: "default"},
	}})
	require.Equal(t, []string{"default", "ops", "web"}, p.all)
}

func TestPage_EnterEmitsDrillRequest(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(DrillRequestMsg)
	require.Equal(t, "web", msg.Receiver)
}

func TestPage_EnterOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd, "Enter on empty list must not panic or emit a drill")
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}, {Name: "c"}}})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, 2, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Equal(t, 0, p.cursor)
}

func TestPage_TitleCarriesCount(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}}})
	require.Equal(t, "receivers[2]", p.Title(),
		"count lives in the title's [N] suffix; HeaderContent stays "+
			"empty so the subtitle line doesn't duplicate it")
	require.Empty(t, p.HeaderContent())
}

func TestPage_RenderShowsRows(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}}})
	out := p.View(40, 10)
	require.Contains(t, out, "ops")
	require.Contains(t, out, "web")
}

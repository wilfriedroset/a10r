// SPDX-License-Identifier: Apache-2.0

package receivers

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
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
	require.Equal(t, []string{"default", "ops", "web"}, p.view,
		"the view is the de-duplicated, scope-filtered union of "+
			"every backend's snapshot — single-backend case lands "+
			"the names sorted alphabetically")
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
	// `gg` is the chord — the dispatcher consumes the first `g`,
	// then resolves to GoToFirstRowMsg on the second. Tests inject
	// the resolved message directly so the assertion is independent
	// of the chord buffer.
	_, _ = p.Update(app.GoToFirstRowMsg{})
	require.Equal(t, 0, p.cursor)
}

func TestPage_FullPageMotionsMoveCursor(t *testing.T) {
	t.Parallel()

	// Build enough rows that the cold-start fallback (20) lands inside
	// the view without clamping.
	p := New(loadStyles(t))
	recs := make([]backend.Receiver, 60)
	for i := range recs {
		recs[i] = backend.Receiver{Name: fmt.Sprintf("r%02d", i)}
	}
	_, _ = p.Update(poll.DataMsg{Resource: recs})

	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "cold-start Ctrl+F falls back to 20 rows")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+B mirrors Ctrl+F")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 10, p.cursor, "cold-start Ctrl+D falls back to 10 rows")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D")

	// Clamps at edges — Ctrl+F at the bottom stays put.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, len(recs)-1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, len(recs)-1, p.cursor,
		"Ctrl+F at the last row clamps; never overshoots")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()

	p := New(loadStyles(t))
	recs := make([]backend.Receiver, 100)
	for i := range recs {
		recs[i] = backend.Receiver{Name: fmt.Sprintf("r%03d", i)}
	}
	_, _ = p.Update(poll.DataMsg{Resource: recs})
	_ = p.View(120, 40) // 40-row body; no header line in receivers

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.cursor, "Ctrl+F walks body-2 from the new cursor (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+B mirrors Ctrl+F symmetrically")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D symmetrically")
}

func TestPage_TitleCarriesCount(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}}})
	require.Equal(t, "receivers(all)[2]", p.Title(),
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

func TestPage_FilterPromptIsLive(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "web"}, {Name: "ops"}, {Name: "default"},
	}})
	require.Len(t, p.view, 3)

	_, _ = p.Update(footer.PromptOpenedMsg{Mode: footer.PromptFilter})
	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "ef"})
	require.Equal(t, []string{"default"}, p.view,
		"live filter must trim the view as the user types")
	require.Equal(t, "receivers(all)[1/3]", p.Title())

	// Cancel reverts to the pre-prompt state.
	_, _ = p.Update(footer.PromptCancelledMsg{Mode: footer.PromptFilter})
	require.Empty(t, p.filter)
	require.Len(t, p.view, 3)
}

// SPDX-License-Identifier: Apache-2.0

package receivers

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestPage_DataMsgSortsReceivers(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
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
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(DrillRequestMsg)
	require.Equal(t, "web", msg.Receiver)
}

func TestPage_EnterOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd, "Enter on empty list must not panic or emit a drill")
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
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
	p := New(testutil.LoadStyles(t))
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

	p := New(testutil.LoadStyles(t))
	recs := make([]backend.Receiver, 100)
	for i := range recs {
		recs[i] = backend.Receiver{Name: fmt.Sprintf("r%03d", i)}
	}
	_, _ = p.Update(poll.DataMsg{Resource: recs})
	_ = p.View(120, 40) // 40-row body; one line goes to the sort header → 39 row budget

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 19, p.cursor, "Ctrl+D walks half the row budget (39 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 56, p.cursor, "Ctrl+F walks budget-2 from the new cursor (19 + 37)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 19, p.cursor, "Ctrl+B mirrors Ctrl+F symmetrically")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D symmetrically")
}

func TestPage_TitleCarriesCount(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}}})
	require.Equal(t, "receivers(all)[2]", p.Title(),
		"count lives in the title's [N] suffix; HeaderContent stays "+
			"empty so the subtitle line doesn't duplicate it")
	require.Empty(t, p.HeaderContent())
}

func TestPage_RenderShowsRows(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}}})
	out := p.View(40, 10)
	require.Contains(t, out, "ops")
	require.Contains(t, out, "web")
}

func TestPage_HeaderRendersForegroundOnly(t *testing.T) {
	t.Parallel()
	// TUI chrome stays on terminal default background — painting
	// palette bg inside the unstyled body frame creates a coloured
	// stripe. Asserts the header line carries no SGR background
	// code.
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}}})
	headerLine, _, _ := strings.Cut(p.View(40, 10), "\n")
	require.NotContains(t, headerLine, "\x1b[48",
		"header must not paint a palette background — chrome stays on terminal default bg")
}

func TestPage_DefaultsToNameAscending(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc(), "alphabetical reading order is the default")
}

func TestPage_SortShortcutTogglesDirection(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "web"}, {Name: "ops"}, {Name: "default"},
	}})
	require.Equal(t, []string{"default", "ops", "web"}, p.view)

	// Same-axis shortcut flips direction; the view flips with it.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.False(t, p.sorter.Asc())
	require.Equal(t, []string{"web", "ops", "default"}, p.view,
		"toggling to DESC reverses the alphabetical view")

	// And toggles back on repeat.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.True(t, p.sorter.Asc())
	require.Equal(t, []string{"default", "ops", "web"}, p.view)
}

func TestPage_SortPreservesCursorOnFocusedReceiver(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "web"}, {Name: "ops"}, {Name: "default"},
	}})
	require.Equal(t, []string{"default", "ops", "web"}, p.view)
	// Walk the cursor onto "ops" then flip to DESC. After the flip
	// the order is web, ops, default — the cursor must follow ops
	// to row 1, not stay on whatever row 1 contained before.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "ops", p.view[p.cursor])
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.Equal(t, []string{"web", "ops", "default"}, p.view)
	require.Equal(t, "ops", p.view[p.cursor],
		"DESC must keep the cursor on the same receiver, not the same index")
}

func TestPage_HLAreNoopOnSingleAxis(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}})
	// Single sortable axis → h/l have nowhere to walk; consume the
	// key but leave direction alone so users don't get a surprise
	// flip from a "previous column" press.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.True(t, p.sorter.Asc(), "l on a single-axis page must NOT flip direction")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.True(t, p.sorter.Asc(), "h on a single-axis page must NOT flip direction")
}

func TestPage_BindingsExposeSortShortcutsForHelpOverlay(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	got := map[string]string{}
	for _, b := range p.Bindings() {
		if strings.HasPrefix(b.Key, "Shift+") {
			got[b.Key] = b.Description
		}
	}
	require.Contains(t, got, "Shift+N",
		"Bindings() must surface Shift+N so the `?` overlay's HOTKEYS column lists it")
	require.Equal(t, "sort by name", got["Shift+N"])
}

func TestPage_HeaderRendersActiveSortArrow(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}}})
	out := testutil.StripStyle(p.View(80, 10))
	require.Contains(t, out, "NAME ↑",
		"default ASC sort must surface an ↑ arrow next to the active axis label")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	out = testutil.StripStyle(p.View(80, 10))
	require.Contains(t, out, "NAME ↓",
		"DESC must surface a ↓ arrow on the same active axis")
}

func TestPage_FilterPromptIsLive(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
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

// TestPage_FilterSearchModesAutodetect pins the receivers page's
// wiring of footer.NewMatcher. Same buffer-mode contract as the
// other list pages — see alerts_test.go for the per-mode rationale.
// Receivers carry only a Name; matching runs against the lower-cased
// name, so the fixture picks names that don't share fuzzy
// subsequences across rows.
func TestPage_FilterSearchModesAutodetect(t *testing.T) {
	t.Parallel()

	receivers := []backend.Receiver{
		{Name: "highcpu"},
		{Name: "web.api"},
		{Name: "diskfull"},
	}

	cases := []struct {
		name      string
		filter    string
		wantNames []string
	}{
		{"tilde flips to fuzzy", "~hgp", []string{"highcpu"}},
		{"backslash forces literal", `\web.api`, []string{"web.api"}},
		{"single dot stays substring", "web.api", []string{"web.api"}},
		{"two metas flip to regex", ".*api", []string{"web.api"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := New(testutil.LoadStyles(t))
			_, _ = p.Update(poll.DataMsg{Resource: receivers})
			_, _ = p.Update(footer.PromptSubmittedMsg{
				Mode: footer.PromptFilter, Value: tc.filter,
			})
			require.ElementsMatch(t, tc.wantNames, p.view)
		})
	}
}

func TestPage_WatchModeToggleSwallowsDataMsg(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	// First snapshot lands normally.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}},
		Tenant:   "prod",
	})
	require.Len(t, p.view, 2, "first DataMsg must populate the view")

	// `w` pauses watch.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.True(t, p.paused, "w must toggle paused on")

	// Subsequent DataMsg is swallowed: view stays at the old snapshot.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}, {Name: "alerts"}},
		Tenant:   "prod",
	})
	require.Len(t, p.view, 2, "paused page must drop incoming DataMsg")

	// `w` again resumes; the next DataMsg lands.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.False(t, p.paused)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}, {Name: "alerts"}},
		Tenant:   "prod",
	})
	require.Len(t, p.view, 3, "resumed page accepts the next DataMsg")
}

func TestPage_WatchModeFooterRendersWatchOff(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}},
		Tenant:   "prod",
	})
	require.NotContains(t, p.Footer(), "WATCH OFF",
		"baseline footer omits WATCH OFF")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.Contains(t, p.Footer(), "WATCH OFF",
		"paused page footer leads with WATCH OFF")
}

func TestPage_WatchModeResumeClearsState(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t))
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.True(t, p.paused)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.False(t, p.paused, "second w returns to running state")
	require.Empty(t, p.Footer(), "resumed page omits WATCH OFF marker")
}

// SPDX-License-Identifier: Apache-2.0

package tenant

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
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
		{Name: "prod", URL: "http://am-prod:9093", Version: "0.27.0"},
		{Name: "staging", URL: "http://am-staging:9093"},
		{Name: "dev", URL: "http://am-dev:9093", Version: "0.26.0"},
	}
}

func TestPage_SetRowsClampsCursor(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())
	p.cursor = 99
	p.SetRows(sampleRows())
	require.Less(t, p.cursor, len(sampleRows()))
}

func TestPage_EnterCallsDrillFactoryWithCursorRowName(t *testing.T) {
	t.Parallel()
	captured := ""
	p := New(Options{
		Styles: loadStyles(t),
		DrillFactory: func(name string) (app.Page, error) {
			captured = name
			// Returning nil page is harmless here: the test only
			// asserts the factory was called with the right name
			// and that a Cmd is produced. The Cmd itself is the
			// app.PushPage signal; its Factory closure isn't
			// invoked until the App's Update consumes it.
			return nil, nil //nolint:nilnil // sentinel for the factory test
		},
	})
	p.SetRows(sampleRows())
	// rowsSorted is alphabetical, so cursor 0 → "dev". Pins the
	// contract: Enter drills against the *visible* cursor row,
	// regardless of insertion order.
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, "dev", captured,
		"the drill factory must be invoked with the cursor row's name")
}

func TestPage_EnterFlashesWhenDrillFactoryErrors(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles: loadStyles(t),
		DrillFactory: func(_ string) (app.Page, error) {
			return nil, errors.New("backend failed to build")
		},
	})
	p.SetRows(sampleRows())
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level,
		"misconfigured backend must flash a warning, not silently crash the inspector")
	require.Contains(t, msg.Text, "backend failed to build")
}

func TestPage_EnterWithoutFactoryIsSilentNoop(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)}) // no DrillFactory
	p.SetRows(sampleRows())
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd,
		"nil DrillFactory is a constructor configuration error; "+
			"Enter cannot recover from it from inside the page")
}

func TestPage_EnterOnEmptyIsNoop(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles: loadStyles(t),
		DrillFactory: func(_ string) (app.Page, error) {
			return nil, nil //nolint:nilnil // sentinel for the empty-rows test
		},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
}

func TestPage_RenderShowsURLAndVersion(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())
	out := testutil.StripStyle(p.View(140, 10))
	require.Contains(t, out, "http://am-prod:9093")
	require.Contains(t, out, "0.27.0")
	require.Contains(t, out, "—",
		"missing version renders as `—` so the column stays aligned")
}

func TestPage_RenderHeaderRowCarriesColumnTitles(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())
	out := testutil.StripStyle(p.View(160, 10))
	for _, want := range []string{"NAME", "URL", "VERSION"} {
		require.Contains(t, out, want, "header row must carry %q", want)
	}
}

func TestPage_DigitsAreNotPageOwned(t *testing.T) {
	t.Parallel()
	// `0`, `1`-`9` are owned by the App's LayerGlobal binding so
	// every page reacts the same way (ScopeChangedMsg). The
	// dispatcher consumes them before forwardToTop runs, so the
	// tenant page must NOT bind them locally — otherwise we'd be
	// chasing two competing handlers per digit.
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())
	for _, code := range []rune{'0', '1', '2', '5', '9'} {
		_, cmd := p.Update(tea.KeyPressMsg{Code: code, Text: string(code)})
		require.Nilf(t, cmd, "digit %q must NOT be locally handled", string(code))
	}
}

func TestPage_RenderShowsEveryRow(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())
	out := p.View(80, 10)
	for _, name := range []string{"prod", "staging", "dev"} {
		require.Contains(t, out, name)
	}
}

func TestPage_TitleAndScopeMirrorGlobal(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())

	// Default scope reads as "all" — matching every other list page.
	require.Equal(t, "tenants(all)[3]", p.Title())

	// `<1>` global quick-switch arrives via ScopeChangedMsg; the
	// page must update its title's `(<scope>)` and tint the in-
	// scope row.
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "prod"})
	require.Equal(t, "tenants(prod)[3]", p.Title())

	out := testutil.StripStyle(p.View(80, 10))
	require.Contains(t, out, "● ",
		"the in-scope row carries a `●` glyph so the user can spot "+
			"which backend is fanned-out without leaving the page")

	// Switching back to all clears the per-row glyph (every row is
	// in scope, so the column reads uniformly).
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "all"})
	require.Equal(t, "tenants(all)[3]", p.Title())
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, len(sampleRows())-1, p.cursor)
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
	// the row list without clamping.
	p := New(Options{Styles: loadStyles(t)})
	rows := make([]Row, 60)
	for i := range rows {
		rows[i] = Row{Name: fmt.Sprintf("t%02d", i), URL: "http://x"}
	}
	p.SetRows(rows)

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
	require.Equal(t, len(rows)-1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, len(rows)-1, p.cursor,
		"Ctrl+F at the last row clamps; never overshoots")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()

	p := New(Options{Styles: loadStyles(t)})
	rows := make([]Row, 100)
	for i := range rows {
		rows[i] = Row{Name: fmt.Sprintf("t%03d", i), URL: "http://x"}
	}
	p.SetRows(rows)
	_ = p.View(120, 41) // 41 - 1 header line = 40-row body

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.cursor, "Ctrl+F walks body-2 from the new cursor (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+B mirrors Ctrl+F symmetrically")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D symmetrically")
}

func TestPage_HeaderContentIsAlwaysEmpty(t *testing.T) {
	t.Parallel()
	// Tenant table is read-only as of #7; nothing to surface in
	// the subtitle line. Pinning this contract so a future
	// regression that re-introduces the legacy mark counter
	// trips the test.
	p := New(Options{Styles: loadStyles(t)})
	p.SetRows(sampleRows())
	require.Empty(t, p.HeaderContent())
}

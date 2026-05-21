// SPDX-License-Identifier: Apache-2.0

package tenant

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func sampleRows() []Row {
	return []Row{
		{Name: "prod", URL: "http://am-prod:9093", Version: "0.27.0"},
		{Name: "staging", URL: "http://am-staging:9093"},
		{Name: "dev", URL: "http://am-dev:9093", Version: "0.26.0"},
	}
}

func TestPage_SetRowsClampsCursor(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())
	p.window.SetIndex(99, len(sampleRows()))
	p.SetRows(sampleRows())
	require.Less(t, p.window.Index(), len(sampleRows()))
}

func TestPage_EnterCallsDrillFactoryWithCursorRowName(t *testing.T) {
	t.Parallel()
	captured := ""
	p := New(Options{
		Styles: testutil.LoadStyles(t),
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
		Styles: testutil.LoadStyles(t),
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
	p := New(Options{Styles: testutil.LoadStyles(t)}) // no DrillFactory
	p.SetRows(sampleRows())
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd,
		"nil DrillFactory is a constructor configuration error; "+
			"Enter cannot recover from it from inside the page")
}

func TestPage_EnterOnEmptyIsNoop(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles: testutil.LoadStyles(t),
		DrillFactory: func(_ string) (app.Page, error) {
			return nil, nil //nolint:nilnil // sentinel for the empty-rows test
		},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd)
}

func TestPage_RenderShowsURLAndVersion(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())
	out := testutil.StripStyle(p.View(140, 10))
	require.Contains(t, out, "http://am-prod:9093")
	require.Contains(t, out, "0.27.0")
	require.Contains(t, out, "—",
		"missing version renders as `—` so the column stays aligned")
}

func TestPage_CanonicalDigitGlyphAnnotatesFirstNine(t *testing.T) {
	t.Parallel()
	// The canonical-digit glyph "[N] " annotates the first 9
	// backends in alphabetical order — the same order the global
	// numeric quick-switch <1>-<9> binds to. The user reads off
	// the digit a row is reachable by without counting positions.
	rows := []Row{
		{Name: "alpha"},
		{Name: "bravo"},
		{Name: "charlie"},
	}
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(rows)
	out := testutil.StripStyle(p.View(120, 10))
	// Each row's prefix region ends with `[N] ● ` (digit + scope
	// glyph + separator) before the NAME column; pin the glyph
	// next to its name to assert the digit attaches to the right
	// canonical row.
	require.Contains(t, out, "[1] ● alpha")
	require.Contains(t, out, "[2] ● bravo")
	require.Contains(t, out, "[3] ● charlie")
}

func TestPage_CanonicalDigitGlyphSkipsRowsPastNine(t *testing.T) {
	t.Parallel()
	// Backends past index 8 (10th row onwards) get a 4-space
	// placeholder so row alignment stays constant; they have no
	// digit binding from the global quick-switch.
	rows := make([]Row, 12)
	for i := range rows {
		rows[i] = Row{Name: fmt.Sprintf("t%02d", i)}
	}
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(rows)
	out := testutil.StripStyle(p.View(160, 14))
	require.Contains(t, out, "[9] ● t08", "9th row in alphabetical order gets [9]")
	require.NotContains(t, out, "[10]", "rows past the 9th must not carry a digit annotation")
	require.NotContains(t, out, "[11]")
	require.NotContains(t, out, "[12]")
}

func TestPage_DigitsAreNotPageOwned(t *testing.T) {
	t.Parallel()
	// `0`, `1`-`9` are owned by the App's LayerGlobal binding so
	// every page reacts the same way (ScopeChangedMsg). The
	// dispatcher consumes them before forwardToTop runs, so the
	// tenant page must NOT bind them locally — otherwise we'd be
	// chasing two competing handlers per digit.
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())
	for _, code := range []rune{'0', '1', '2', '5', '9'} {
		_, cmd := p.Update(tea.KeyPressMsg{Code: code, Text: string(code)})
		require.Nilf(t, cmd, "digit %q must NOT be locally handled", string(code))
	}
}

func TestPage_RenderShowsEveryRow(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())
	out := p.View(80, 10)
	for _, name := range []string{"prod", "staging", "dev"} {
		require.Contains(t, out, name)
	}
}

func TestPage_TitleAndScopeMirrorGlobal(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
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

// TestPage_VimMotions is the wiring smoke for the cursor module:
// pressing `j` in Update must route into Window.MoveCursor. The
// full motion contract (j/k/G/g/Ctrl+D/U/F/B, clamps, empty-view)
// lives in internal/tui/page/cursor/window_test.go:TestWindow_MoveCursor;
// this test only proves the page is wired to it.
func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.window.Index(), "Update must route `j` into Window.MoveCursor")
}

func TestSemverLess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a, b string
		want bool
	}{
		{"0.9.0", "0.27.0", true},     // numeric segments: 9 < 27
		{"0.27.0", "0.9.0", false},    // reverse — 27 not < 9
		{"1.2.3", "1.10.0", true},     // the bug a default string sort makes
		{"v0.28.1", "0.28.1", false},  // leading 'v' stripped → equal → false
		{"0.28.1", "v0.28.1", false},  // symmetric
		{"0.27.0", "0.27.1", true},    // last numeric segment differs
		{"2.13.0", "0.27.0", false},   // major dominates
		{"0.27.0", "2.13.0", true},    // major dominates — reverse
		{"1.0.0", "1.0.0-rc.1", true}, // shorter parts sort before longer
	}
	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, semverLess(tc.a, tc.b),
				"semverLess(%q, %q)", tc.a, tc.b)
		})
	}
}

func TestPage_DefaultSortIsNameAsc(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc())
}

func TestPage_ShiftVSortsByVersionSemverAware(t *testing.T) {
	t.Parallel()
	// Insertion order shows the bug a lexical sort would hide:
	// "0.9.0" sorts after "0.27.0" lexically, but semver-correct
	// ordering puts 0.9.0 ahead. The `Shift+V` keystroke must
	// yield the semver-correct order.
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows([]Row{
		{Name: "alpha", Version: "0.27.0"},
		{Name: "bravo", Version: "0.9.0"},
		{Name: "charlie", Version: "1.10.0"},
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	out := testutil.StripStyle(p.View(120, 10))
	// In ASC mode the order should be 0.9.0, 0.27.0, 1.10.0.
	bravo := strings.Index(out, "bravo")
	alpha := strings.Index(out, "alpha")
	charlie := strings.Index(out, "charlie")
	require.Less(t, bravo, alpha, "0.9.0 must sort before 0.27.0 (semver-aware)")
	require.Less(t, alpha, charlie, "0.27.0 must sort before 1.10.0")
}

func TestPage_VersionSortPutsEmptyLastInAsc(t *testing.T) {
	t.Parallel()
	// Empty version is "unknown", not "lowest" — concrete numbers
	// surface at the top in ASC (operator triaging stale backends
	// reads top-down looking for real data).
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows([]Row{
		{Name: "alpha", Version: "0.27.0"},
		{Name: "bravo"}, // empty version
		{Name: "charlie", Version: "0.9.0"},
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	out := testutil.StripStyle(p.View(120, 10))
	charlie := strings.Index(out, "charlie")
	alpha := strings.Index(out, "alpha")
	bravo := strings.Index(out, "bravo")
	require.Less(t, charlie, alpha, "0.9.0 sorts before 0.27.0 (semver)")
	require.Less(t, alpha, bravo, "non-empty versions sort before empty/unknown")
}

func TestPage_VersionSortPutsEmptyLastInDescToo(t *testing.T) {
	t.Parallel()
	// Symmetric to the ASC case: empty version is "unknown", and
	// the operator scanning DESC for the newest backends should
	// not see unknown rows at the top — concrete numbers stay at
	// the top regardless of direction. rowsSorted post-processes
	// empties to the bottom so this holds without a direction-
	// aware Less function.
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows([]Row{
		{Name: "alpha", Version: "0.27.0"},
		{Name: "bravo"}, // empty version
		{Name: "charlie", Version: "0.9.0"},
	})
	// Two presses → DESC.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	out := testutil.StripStyle(p.View(120, 10))
	alpha := strings.Index(out, "alpha")
	charlie := strings.Index(out, "charlie")
	bravo := strings.Index(out, "bravo")
	require.Less(t, alpha, charlie, "DESC: 0.27.0 sorts before 0.9.0")
	require.Less(t, charlie, bravo,
		"empty version stays last even in DESC — unknown is not 'highest'")
}

func TestPage_DigitAnnotationStaysCanonicalAfterReSort(t *testing.T) {
	t.Parallel()
	// The whole point of the canonical digit annotation: re-sorting
	// the visible rows must NOT change which row wears [1] / [2] / [3].
	// alpha wears [1] when alphabetical-first, regardless of where
	// the visible sort puts it.
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows([]Row{
		{Name: "alpha", Version: "0.27.0"},
		{Name: "bravo", Version: "0.9.0"},
		{Name: "charlie", Version: "1.0.0"},
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	out := testutil.StripStyle(p.View(120, 10))
	// Visible (V ASC): bravo (0.9.0), alpha (0.27.0), charlie (1.0.0).
	// Canonical digits: alpha=[1], bravo=[2], charlie=[3].
	require.Contains(t, out, "[1] ● alpha",
		"[1] stays on alpha (alphabetical-first) regardless of visible sort")
	require.Contains(t, out, "[2] ● bravo")
	require.Contains(t, out, "[3] ● charlie")
}

func TestPage_UserReSortKeepsCursorAtRowIndex(t *testing.T) {
	t.Parallel()
	// k9s-positional contract: cursor stays at the same row index
	// when the user re-sorts. Tenant has no focusName-restore, so
	// this is automatic — but pin it as a test so a future
	// "restore cursor by name" regression is caught.
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows([]Row{
		{Name: "alpha", Version: "0.27.0"},
		{Name: "bravo", Version: "0.9.0"},
		{Name: "charlie", Version: "1.0.0"},
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.window.Index())
	require.Equal(t, "bravo", p.rowsSorted()[p.window.Index()].Name)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	require.Equal(t, 1, p.window.Index(), "cursor stays at row index on user re-sort")
	// V ASC: bravo, alpha, charlie. cursor 1 is now alpha.
	require.Equal(t, "alpha", p.rowsSorted()[p.window.Index()].Name)
}

func TestPage_HeaderRendersForegroundOnly(t *testing.T) {
	t.Parallel()
	// TUI chrome stays on terminal default background — painting
	// palette bg inside the unstyled body frame creates a coloured
	// stripe. Asserts the header line carries no SGR background
	// code (covers both inactive Header fg and active HeaderActive
	// fg paths).
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())
	headerLine, _, _ := strings.Cut(p.View(120, 10), "\n")
	require.NotContains(t, headerLine, "\x1b[48",
		"header must not paint a palette background — chrome stays on terminal default bg")
}

func TestPage_HeaderContentIsAlwaysEmpty(t *testing.T) {
	t.Parallel()
	// Tenant table is read-only; nothing to surface in the
	// subtitle line. Pinning this contract so a future regression
	// that re-introduces the legacy mark counter trips the test.
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.SetRows(sampleRows())
	require.Empty(t, p.HeaderContent())
}

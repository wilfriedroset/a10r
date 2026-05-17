// SPDX-License-Identifier: Apache-2.0

package receivers

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestPage_DataMsgSortsReceivers(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd().(DrillRequestMsg)
	require.Equal(t, "web", msg.Receiver)
}

func TestPage_EnterOnEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, cmd, "Enter on empty list must not panic or emit a drill")
}

// TestPage_VimMotions is the wiring smoke for the cursor module:
// pressing `j` in Update must route into cursor.HandleMotion. The
// full motion contract (j/k/G/g/Ctrl+D/U/F/B, clamps, empty-view)
// lives in internal/tui/page/cursor/motion_test.go:TestHandleMotion;
// this test only proves the page is wired to it.
func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}, {Name: "c"}}})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.cursor, "Update must route `j` into cursor.HandleMotion")
}

func TestPage_TitleCarriesCount(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}}})
	require.Equal(t, "receivers(all)[2]", p.Title(),
		"count lives in the title's [N] suffix; HeaderContent stays "+
			"empty so the subtitle line doesn't duplicate it")
	require.Empty(t, p.HeaderContent())
}

func TestPage_RenderShowsRows(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}}})
	headerLine, _, _ := strings.Cut(p.View(40, 10), "\n")
	require.NotContains(t, headerLine, "\x1b[48",
		"header must not paint a palette background — chrome stays on terminal default bg")
}

func TestPage_DefaultsToNameAscending(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc(), "alphabetical reading order is the default")
}

func TestPage_SortShortcutTogglesDirection(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
			p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
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
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.True(t, p.paused)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.False(t, p.paused, "second w returns to running state")
	require.Empty(t, p.Footer(), "resumed page omits WATCH OFF marker")
}

func TestPage_ErrorBandReflectsBackendStatusDetail(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	// Single-tenant scope: detail is rendered verbatim without a
	// tenant prefix. The page constructor seeds scope to "all" by
	// default, so we narrow it for this case.
	p.scope = "prod"

	require.Empty(t, p.ErrorBand())

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "connection refused",
	})
	require.Equal(t, "connection refused", p.ErrorBand(),
		"single-tenant scope renders detail verbatim (no tenant prefix)")

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnConnected,
		Detail: "",
	})
	require.Empty(t, p.ErrorBand(),
		"recovery clears the band so transient blips don't linger")
}

func TestPage_ErrorBandPrefixesTenantOnAllScope(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "401 unauthorised",
	})
	require.Equal(t, "prod: 401 unauthorised", p.ErrorBand(),
		"all-scope view prefixes tenant so the operator knows which one")
}

func TestPage_ErrorBandCollapsesMultipleOffenders(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})

	_, _ = p.Update(poll.BackendStatusMsg{Tenant: "alpha", State: header.ConnUnreachable, Detail: "down"})
	_, _ = p.Update(poll.BackendStatusMsg{Tenant: "beta", State: header.ConnUnreachable, Detail: "401"})

	require.Equal(t, "2 backends erroring; alpha: down", p.ErrorBand())
}

func TestPage_ErrorBandExcludesOutOfScopeTenants(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	p.scope = "prod"

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "staging",
		State:  header.ConnUnreachable,
		Detail: "should not appear",
	})
	require.Empty(t, p.ErrorBand(),
		"out-of-scope tenant errors must not bleed into the band")

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "in scope",
	})
	require.Equal(t, "in scope", p.ErrorBand(),
		"in-scope error surfaces verbatim under a single-tenant scope")
}

// TestPage_DropsDataMsgFromUnknownTenant pins that DataMsg /
// BackendStatusMsg arriving with a tenant name not in the configured
// list is dropped — closes the same leak class the alerts / silences /
// groups pages already guard against. The receivers page brainstorm
// flagged "lastErrors not pruned" / "byTenant retains entries for
// tenants no longer in scope"; this completes the carve-out from
// 25d1640 that originally skipped receivers because the page didn't
// yet carry a tenants list. Empty Tenants disables the guard so
// existing tests without an explicit list keep working.
func TestPage_DropsDataMsgFromUnknownTenant(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  testutil.LoadStyles(t),
		Tenants: []string{"prod", "staging"},
	})
	// Known tenant — should land.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}},
		Tenant:   "prod",
	})
	require.Contains(t, p.byTenant, "prod",
		"known tenant must be accepted into byTenant")

	// Unknown tenant — must be dropped.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "stray"}},
		Tenant:   "ghost",
	})
	require.NotContains(t, p.byTenant, "ghost",
		"unknown tenant must not populate byTenant")

	// BackendStatusMsg for unknown tenant must also drop.
	_, _ = p.Update(poll.BackendStatusMsg{Tenant: "ghost", Detail: "unreachable"})
	require.NotContains(t, p.lastErrors, "ghost",
		"unknown tenant must not populate lastErrors")
}

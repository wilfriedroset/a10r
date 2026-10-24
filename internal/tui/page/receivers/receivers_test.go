// SPDX-License-Identifier: Apache-2.0

package receivers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

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
// pressing `j` in Update must route into Window.MoveCursor. The
// full motion contract (j/k/G/g/Ctrl+D/U/F/B, clamps, empty-view)
// lives in internal/tui/page/cursor/window_test.go:TestWindow_MoveCursor;
// this test only proves the page is wired to it.
func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "a"}, {Name: "b"}, {Name: "c"}}})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.Index(), "Update must route `j` into Window.MoveCursor")
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

// A sort keystroke keeps the row position, as k9s does, while a poll
// refresh follows the focused receiver (see
// TestPage_PollKeepsCursorOnFocusedReceiver).
func TestPage_UserResortKeepsCursorAtRowIndex(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "web"}, {Name: "ops"}, {Name: "default"},
	}})
	require.Equal(t, []string{"default", "ops", "web"}, viewNames(p))
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.Equal(t, []string{"web", "ops", "default"}, viewNames(p))
	require.Equal(t, 0, p.Index())
	require.Equal(t, "web", p.view[p.Index()].name)
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

// TestPage_FilterNarrowsView is the per-page wiring smoke proving
// the receivers page reads its compiled filter into p.view, mode and
// all. The mode-autodetect contract lives in
// internal/tui/filter/{searchmode,matcher}_test.go, and the
// live-narrow / Esc-restore / submit-empty-clears contract in
// internal/tui/footer/footer_test.go (TestPrompt_* family). This
// test only proves the wiring exists.
func TestPage_FilterNarrowsView(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
		{Name: "web"}, {Name: "ops"}, {Name: "default"},
	}})
	require.Len(t, p.view, 3)

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "ef"})
	require.Equal(t, []string{"default"}, viewNames(p),
		"submitted filter must trim the view to the matching row")
	require.Equal(t, "receivers(all)[1/3]", p.Title())

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "~dfl"})
	require.Equal(t, []string{"default"}, viewNames(p),
		"the sigil the buffer was classified with must reach the row loop")
}

// TestPage_WatchModeFooterRendersWatchOff is the page-specific wiring
// witness for the watch/pause-refresh contract. The full contract
// (DataMsg swallowed while paused, manual `r` honoured once,
// resume-clears-state) is covered canonically by
// internal/tui/page/alerts/alerts_test.go:TestPage_WatchModeToggleSwallowsDataMsg
// / TestPage_WatchModeManualRefreshHonouredOnce — this smoke just
// proves the receivers page's Update loop dispatches `w` so a wire
// regression here still red-lights.
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

func TestPage_ErrorBandReflectsBackendStatusDetail(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	p := New(Options{Styles: testutil.LoadStyles(t)})
	// Single-tenant scope: detail is rendered verbatim without a
	// tenant prefix. The page constructor seeds scope to "all" by
	// default, so we narrow it for this case.
	p.Scope = "prod"

	require.Empty(t, p.ErrorBand(now))

	// NextAt is past-due (zero) so the suffix renders as `retrying now`.
	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "connection refused",
	})
	require.Equal(t, "connection refused — retrying now", p.ErrorBand(now),
		"single-tenant scope renders detail verbatim (no tenant prefix) with the retry suffix")

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnConnected,
	})
	require.Empty(t, p.ErrorBand(now),
		"recovery clears the band so transient blips don't linger")
}

// TestPage_DropsDataMsgFromUnknownTenant pins that DataMsg /
// BackendStatusMsg arriving with a tenant name not in the
// configured list is dropped — the same leak class the alerts /
// silences pages already guard against (BackendHealth not
// pruned, byTenant retaining entries for tenants no longer in
// scope). Empty Tenants disables the guard so existing tests
// without an explicit list keep working.
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
	require.NotContains(t, p.BackendHealth, "ghost",
		"unknown tenant must not populate BackendHealth")
}

// TestPage_TenantColumnAppearsForAllScope mirrors the alerts /
// silences contract: when the active scope is "all" and the
// configured fleet spans more than one backend, a leading TENANT
// column surfaces so the user knows which backend each receiver
// came from. Canonical predicate coverage lives in
// internal/tui/page/listpage/show_tenant_column_test.go; this proves
// the receivers page wires it into the header and rows.
func TestPage_TenantColumnAppearsForAllScope(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  testutil.LoadStyles(t),
		Tenants: []string{"prod", "staging"},
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}},
		Tenant:   "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "web"}},
		Tenant:   "staging",
	})

	out := testutil.StripStyle(p.View(80, 20))
	require.Contains(t, out, "TENANT", "all-scope multi-tenant fleet must show the TENANT column")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "staging")
	require.Contains(t, out, "ops")
	require.Contains(t, out, "web")
}

// TestPage_TenantColumnHiddenForSingleBackend pins that a single-
// backend fleet keeps the trivial single-column layout — the TENANT
// column would be dead chrome with nothing to disambiguate.
func TestPage_TenantColumnHiddenForSingleBackend(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  testutil.LoadStyles(t),
		Tenants: []string{"prod"},
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "ops"}},
		Tenant:   "prod",
	})

	out := testutil.StripStyle(p.View(80, 20))
	require.NotContains(t, out, "TENANT",
		"single-backend setups must NOT show the tenant column")
}

// TestPage_PerTenantRowsNotDeduplicated is the behaviour change: a
// receiver name shared across two backends renders one row per
// tenant (tagged by the TENANT column) rather than collapsing into
// a single de-duplicated entry — matching how alerts / silences
// present cross-tenant data.
func TestPage_PerTenantRowsNotDeduplicated(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  testutil.LoadStyles(t),
		Tenants: []string{"prod", "staging"},
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "default"}},
		Tenant:   "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Receiver{{Name: "default"}},
		Tenant:   "staging",
	})

	require.Equal(t, []string{"default", "default"}, viewNames(p),
		"a receiver shared across tenants must yield one row per tenant")
	require.Equal(t, "receivers(all)[2]", p.Title(),
		"the count reflects per-tenant rows, not the de-duplicated union")
	require.Equal(t, []string{"prod", "staging"}, []string{p.view[0].tenant, p.view[1].tenant},
		"a shared name breaks the tie by tenant, ascending")
}

func TestPage_PollKeepsCursorOnFocusedReceiver(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		keys      string
		next      []backend.Receiver
		want      string
		wantIndex int
	}{
		{
			name: "row inserted above the cursor",
			next: []backend.Receiver{{Name: "alpha"}, {Name: "default"}, {Name: "ops"}, {Name: "web"}},
			want: "default", wantIndex: 1,
		},
		{
			name: "row removed above the cursor",
			keys: "j",
			next: []backend.Receiver{{Name: "ops"}, {Name: "web"}},
			want: "ops", wantIndex: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := New(Options{Styles: testutil.LoadStyles(t)})
			_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{
				{Name: "web"}, {Name: "ops"}, {Name: "default"},
			}})
			for _, k := range tt.keys {
				_, _ = p.Update(tea.KeyPressMsg{Code: k, Text: string(k)})
			}
			_, _ = p.Update(poll.DataMsg{Resource: tt.next})
			require.Equal(t, tt.wantIndex, p.Index())
			require.Equal(t, tt.want, p.view[p.Index()].name)
		})
	}
}

func TestPage_TitleNamesTheScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		scope string
		want  string
	}{
		{scope: "", want: "receivers(all)[1]"},
		{scope: "prod", want: "receivers(prod)[1]"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			p := New(Options{Styles: testutil.LoadStyles(t)})
			p.Scope = tt.scope
			_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}}, Tenant: "prod"})
			require.Equal(t, tt.want, p.Title())
		})
	}
}

func TestPage_EmptyStateLinesAndTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		recs   []backend.Receiver
		filter string
		down   bool
		title  string
		want   []string
	}{
		{
			name:   "filter hides every row",
			recs:   []backend.Receiver{{Name: "ops"}, {Name: "web"}},
			filter: "zzz",
			title:  "receivers(prod)[0/2]",
			want:   []string{"no receivers match the active filter — Esc clears the prompt"},
		},
		{
			name:   "filter before any poll",
			filter: "zzz",
			title:  "receivers(prod)[0/0]",
			want:   []string{"no receivers (yet)"},
		},
		{
			name:  "error band above the hint",
			down:  true,
			title: "receivers(prod)[0]",
			want:  []string{"! down — retrying now", "no receivers (yet)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := New(Options{Styles: testutil.LoadStyles(t)})
			p.Scope = "prod"
			if tt.recs != nil {
				_, _ = p.Update(poll.DataMsg{Resource: tt.recs, Tenant: "prod"})
			}
			if tt.down {
				_, _ = p.Update(poll.BackendStatusMsg{Tenant: "prod", State: header.ConnUnreachable, Detail: "down"})
			}
			if tt.filter != "" {
				_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: tt.filter})
			}
			require.Equal(t, tt.title, p.Title())
			lines := strings.Split(testutil.StripStyle(p.View(80, 5)), "\n")
			for i, l := range lines {
				lines[i] = strings.TrimSpace(l)
			}
			require.Equal(t, tt.want, lines[:len(tt.want)])
		})
	}
}

func TestPage_ViewEmptyWhenSizeIsZero(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}}})
	require.Empty(t, p.View(0, 10))
	require.Empty(t, p.View(40, 0))
}

func TestPage_ViewScrollsToCursorWithinHeight(t *testing.T) {
	t.Parallel()
	recs := make([]backend.Receiver, 10)
	for i := range recs {
		recs[i] = backend.Receiver{Name: fmt.Sprintf("r%d", i)}
	}
	tests := []struct {
		name   string
		band   bool
		height int
		top    []string
		bottom []string
	}{
		{
			name:   "no band",
			height: 4,
			top:    []string{"NAME ↑", "▸ r0", "r1", "r2"},
			bottom: []string{"NAME ↑", "r7", "r8", "▸ r9"},
		},
		{
			name:   "error band takes a row",
			band:   true,
			height: 5,
			top:    []string{"NAME ↑", "▸ r0", "r1", "r2"},
			bottom: []string{"NAME ↑", "r7", "r8", "▸ r9"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := New(Options{Styles: testutil.LoadStyles(t)})
			p.Scope = "prod"
			_, _ = p.Update(poll.DataMsg{Resource: recs, Tenant: "prod"})
			if tt.band {
				_, _ = p.Update(poll.BackendStatusMsg{Tenant: "prod", State: header.ConnUnreachable, Detail: "down"})
			}
			bodyLines := func() []string {
				lines := strings.Split(testutil.StripStyle(p.View(40, tt.height)), "\n")
				if tt.band {
					require.True(t, strings.HasPrefix(lines[0], "! down"), lines[0])
					lines = lines[1:]
				}
				for i, l := range lines {
					lines[i] = strings.TrimSpace(l)
				}
				return lines
			}
			require.Equal(t, tt.top, bodyLines())
			_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
			require.Equal(t, tt.bottom, bodyLines())
		})
	}
}

func TestPage_CursorStyleOnlyOnCursorRow(t *testing.T) {
	t.Parallel()
	p := New(Options{Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Receiver{{Name: "ops"}, {Name: "web"}}})
	lines := strings.Split(p.View(40, 5), "\n")
	require.Len(t, lines, 3)
	require.NotEqual(t, testutil.StripStyle(lines[1]), lines[1], "cursor row is styled")
	require.Equal(t, testutil.StripStyle(lines[2]), lines[2], "plain row carries no style")
}

// viewNames projects the page's view onto receiver names so the
// table-shape assertions stay readable now that the view holds
// per-tenant entries rather than bare strings.
func viewNames(p *Page) []string {
	out := make([]string, len(p.view))
	for i, e := range p.view {
		out[i] = e.name
	}
	return out
}

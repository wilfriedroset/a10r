// SPDX-License-Identifier: Apache-2.0

package status

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

func sampleStatus() backend.Status {
	return backend.Status{
		Cluster: backend.ClusterStatus{
			Status: "ready",
			Peers:  []backend.ClusterPeer{{Name: "node-a", Address: "10.0.0.1:9094"}},
		},
		Version: backend.VersionInfo{Version: "0.28.1", Revision: "abc123"},
		Config:  "route:\n  receiver: web\nreceivers:\n  - name: web\n",
		Uptime:  3 * time.Hour,
	}
}

func TestPage_RenderShowsAllSections(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})
	out := p.View(120, 50)
	for _, want := range []string{
		"Cluster", "ready", "node-a",
		"Version", "0.28.1", "abc123",
		"Config", "receiver: web",
	} {
		require.Contains(t, out, want, "missing %q", want)
	}
}

func TestPage_AnchorJumpsToSection(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	require.Positive(t, p.scroll,
		"`p` must scroll past the cluster + version sections to the config")
	out := p.View(120, 5)
	require.Contains(t, out, "Config")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	require.Equal(t, 0, p.scroll, "`c` must scroll back to the cluster section at the top")
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Positive(t, p.scroll)
	// `gg` is the chord — the dispatcher consumes the first `g`,
	// then resolves to GoToFirstRowMsg on the second. Tests inject
	// the resolved message directly because the dispatcher's
	// chord buffer is wired in cmd/tui.go, not in the page.
	_, _ = p.Update(app.GoToFirstRowMsg{})
	require.Equal(t, 0, p.scroll)
}

func TestPage_FullPageMotions(t *testing.T) {
	t.Parallel()
	// Cold-start path (no View call yet): keystrokes fall back to
	// the 20-row step so the very first input still moves a sane
	// distance before bubbletea has ticked a render.
	st := sampleStatus()
	long := make([]string, 0, 100)
	for i := range 100 {
		long = append(long, "  line-"+string(rune('a'+i%26)))
	}
	st.Config = "route:\n" + strings.Join(long, "\n") + "\n"
	p := New(loadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: st})

	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "cold-start Ctrl+F falls back to 20 lines")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+B mirrors Ctrl+F")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+B clamps at 0; never goes negative")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()
	st := sampleStatus()
	long := make([]string, 0, 200)
	for i := range 200 {
		long = append(long, "  line-"+string(rune('a'+i%26)))
	}
	st.Config = "route:\n" + strings.Join(long, "\n") + "\n"
	p := New(loadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: st})
	_ = p.View(120, 40) // 40-line viewport — vim's full-page = 40-2 = 38, half = 20

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.scroll, "Ctrl+F walks body-2 (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll)
}

func TestPage_HeaderContentBeforeAndAfterData(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "prod")
	require.Contains(t, p.HeaderContent(), "loading")

	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})
	require.Contains(t, p.HeaderContent(), "0.28.1")
	require.Contains(t, p.HeaderContent(), "prod")
}

func TestPage_EmptyView(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "")
	out := p.View(80, 5)
	require.Contains(t, out, "no data")
}

func TestPage_HandlesNilDataMsg(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "")
	_, _ = p.Update(poll.DataMsg{Resource: "wrong type"})
	require.False(t, p.have, "wrong-typed Resource must be ignored")
}

func TestPage_ConfigPreservesNewlines(t *testing.T) {
	t.Parallel()
	p := New(loadStyles(t), "")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})
	out := strings.Join(p.lines(), "\n")
	require.Contains(t, out, "route:\n  receiver: web")
}

func TestPage_TitleFollowsScopeChange(t *testing.T) {
	t.Parallel()
	// Empty constructor scope reads as "all" — same convention as
	// the alerts page so the title shape is uniform across views.
	p := New(loadStyles(t), "")
	require.Equal(t, "status(all)", p.Title())

	// A global numeric quick-switch (ScopeChangedMsg) updates the
	// title's `(<scope>)` segment immediately.
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "prod"})
	require.Equal(t, "status(prod)", p.Title())

	_, _ = p.Update(app.ScopeChangedMsg{Scope: "all"})
	require.Equal(t, "status(all)", p.Title())
}

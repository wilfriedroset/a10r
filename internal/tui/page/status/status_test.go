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
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

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
	p := New(testutil.LoadStyles(t), "prod")
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
	p := New(testutil.LoadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	require.Positive(t, p.scroll,
		"`p` must scroll past the cluster + version sections to the config")
	out := p.View(120, 5)
	require.Contains(t, out, "Config")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	require.Equal(t, 0, p.scroll, "`c` must scroll back to the cluster section at the top")
}

// TestPage_VimMotions is the wiring smoke for the cursor module:
// pressing `j` in Update must route into cursor.HandleMotion and
// advance p.scroll. The full motion contract (j/k/G/g/Ctrl+D/U/F/B,
// clamps, empty-view) lives in
// internal/tui/page/cursor/motion_test.go:TestHandleMotion; this
// test only proves the page is wired to it.
func TestPage_VimMotions(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "prod")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.scroll, "Update must route `j` into cursor.HandleMotion")
}

func TestPage_HeaderContentBeforeAndAfterData(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "prod")
	require.Contains(t, p.HeaderContent(), "loading")

	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})
	require.Contains(t, p.HeaderContent(), "0.28.1")
	require.Contains(t, p.HeaderContent(), "prod")
}

// TestPage_HeaderContent_HumanisesUptime is the regression test for
// the status brainstorm finding HeaderContent_FormatsUptime_AsGoDurationString
// at status.go:71: a 10-year uptime used to render as the raw Go
// time.Duration Stringer "87600h0m0s", which is hostile UX for an
// SRE-targeted TUI. The fix routes Uptime through timerender.Duration
// so the header zone shows compact units (s/m/h/d).
func TestPage_HeaderContent_HumanisesUptime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		uptime time.Duration
		want   string
	}{
		{name: "3h compact", uptime: 3 * time.Hour, want: "3h"},
		{name: "5d compact", uptime: 5 * 24 * time.Hour, want: "5d"},
		{name: "10y compact", uptime: 10 * 365 * 24 * time.Hour, want: "3650d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := New(testutil.LoadStyles(t), "prod")
			st := sampleStatus()
			st.Uptime = tc.uptime
			_, _ = p.Update(poll.DataMsg{Resource: st})

			out := p.HeaderContent()
			require.Contains(t, out, "uptime "+tc.want,
				"uptime must be humanised, not rendered as a raw Go duration")
			require.NotRegexp(t, `\d+h\d+m\d+s`, out,
				"uptime must NOT appear as a raw `Nh0m0s` Go Stringer string")
		})
	}
}

func TestPage_EmptyView(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "")
	out := p.View(80, 5)
	require.Contains(t, out, "no data")
}

func TestPage_HandlesNilDataMsg(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "")
	_, _ = p.Update(poll.DataMsg{Resource: "wrong type"})
	require.False(t, p.have, "wrong-typed Resource must be ignored")
}

func TestPage_ConfigPreservesNewlines(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "")
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})
	out := strings.Join(p.lines(), "\n")
	require.Contains(t, out, "route:\n  receiver: web")
}

// TestPage_PollResourcesIncludesStatus is the regression test for
// the brainstorm finding Page_NeverRefreshes_AfterStartup: the
// status page previously returned an empty PollResources slice,
// which combined with the one-shot Init fetch left the page
// rendering a stale version / uptime / config for the entire
// session. Declaring the "status" resource here lets the wire
// layer's poll machinery route DataMsg{Resource: backend.Status}
// at the configured interval — the dynamic uptime line in
// HeaderContent then actually ticks instead of freezing on the
// first reading.
func TestPage_PollResourcesIncludesStatus(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "prod")
	require.Equal(t, []string{"status"}, p.PollResources(),
		"status page must declare the `status` resource so the wire-layer "+
			"poller routes DataMsg{Resource: backend.Status} into the page")
}

// TestPage_DataMsgUpdatesStatusFromPoll covers the periodic-refresh
// path: a DataMsg arriving after the cold-start primer must replace
// the cached backend.Status so the next render reflects the freshest
// uptime / version / config. Without this branch the page would
// still render the first snapshot for the whole session even when
// PollResources advertises the resource.
func TestPage_DataMsgUpdatesStatusFromPoll(t *testing.T) {
	t.Parallel()
	p := New(testutil.LoadStyles(t), "prod")

	// First poll lands the cold-start payload.
	_, _ = p.Update(poll.DataMsg{Resource: sampleStatus()})
	require.True(t, p.have, "first DataMsg must mark page as primed")
	require.Equal(t, "0.28.1", p.st.Version.Version)
	require.Equal(t, 3*time.Hour, p.st.Uptime)

	// Second poll lands a fresher snapshot — the page must replace
	// the cached struct, not keep the old one.
	fresh := sampleStatus()
	fresh.Version.Version = "0.29.0"
	fresh.Uptime = 4 * time.Hour
	_, _ = p.Update(poll.DataMsg{Resource: fresh})
	require.Equal(t, "0.29.0", p.st.Version.Version,
		"subsequent DataMsg must overwrite the cached status so the "+
			"page renders the freshest version, not the cold-start one")
	require.Equal(t, 4*time.Hour, p.st.Uptime,
		"uptime must tick across polls so the humanised label updates")
}

func TestPage_TitleFollowsScopeChange(t *testing.T) {
	t.Parallel()
	// Empty constructor scope reads as "all" — same convention as
	// the alerts page so the title shape is uniform across views.
	p := New(testutil.LoadStyles(t), "")
	require.Equal(t, "status(all)", p.Title())

	// A global numeric quick-switch (ScopeChangedMsg) updates the
	// title's `(<scope>)` segment immediately.
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "prod"})
	require.Equal(t, "status(prod)", p.Title())

	_, _ = p.Update(app.ScopeChangedMsg{Scope: "all"})
	require.Equal(t, "status(all)", p.Title())
}

// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// Run `go test ./internal/tui/boot -update -run TestGoldenFrame` to
// rewrite every frame golden after an intentional layout change.
var updateGolden = flag.Bool("update", false, "rewrite golden files under testdata/")

// frameClock is the instant every frame renders at. Fixtures are
// expressed relative to it so the AGE and ENDS IN columns are
// byte-stable across runs.
var frameClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// frameClient answers the one Status call Build makes per tenant
// to fill the tenant rows and fails the test on every list fetch. Pages
// render from the canned poll.DataMsg values the harness replays,
// so any list call is a fetch path that bypassed them.
type frameClient struct {
	*fakeStatusBackend
	t *testing.T
}

func (c frameClient) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	c.t.Error("ListAlerts: the frame harness must not reach a backend")
	return nil, nil
}

func (c frameClient) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	c.t.Error("ListSilences: the frame harness must not reach a backend")
	return nil, nil
}

func (c frameClient) ListReceivers(context.Context) ([]backend.Receiver, error) {
	c.t.Error("ListReceivers: the frame harness must not reach a backend")
	return nil, nil
}

// frameConfig is the two-tenant configuration every golden frame
// renders against. Two backends keep the TENANT column and the
// numeric quick-switch row non-trivial.
func frameConfig() *config.Config {
	return &config.Config{
		Backends: []config.Backend{
			{Name: "prod", URL: "https://am-prod.internal"},
			{Name: "staging", URL: "https://am-staging.internal"},
		},
	}
}

// framePollData is the canned poll payload set the harness replays
// in place of the poller matrix: one DataMsg per (resource, tenant)
// pair that startBackendPoller would emit.
func framePollData() []poll.DataMsg {
	msg := func(resource, tenant string, payload any) poll.DataMsg {
		return poll.DataMsg{
			Resource:      payload,
			Tenant:        tenant,
			ResourceLabel: resource,
			At:            frameClock,
			NextAt:        frameClock.Add(time.Minute),
		}
	}
	alert := func(name, severity string, age time.Duration, extra map[string]string) backend.Alert {
		return pagetest.Alert(pagetest.AlertOptions{
			Name: name, Severity: severity, Now: frameClock, Age: age,
			Fingerprint: name + severity, Labels: extra,
		})
	}
	silence := func(id, by string, endsIn time.Duration) backend.Silence {
		return pagetest.Silence(pagetest.SilenceOptions{
			ID: id, CreatedBy: by, Now: frameClock, EndsIn: endsIn,
			Comment:  "scheduled maintenance",
			Matchers: []backend.Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
		})
	}
	status := backend.Status{
		Cluster: backend.ClusterStatus{
			Status: "ready",
			Peers:  []backend.ClusterPeer{{Name: "01ABC", Address: "10.0.0.1:9094"}},
		},
		Version: backend.VersionInfo{Version: "0.28.0", Branch: "HEAD", GoVersion: "go1.25.0"},
		Uptime:  72 * time.Hour,
	}

	return []poll.DataMsg{
		msg(resourceAlerts, "prod", []backend.Alert{
			alert("HighCPU", "critical", 12*time.Minute, map[string]string{"instance": "web-01"}),
			alert("HighCPU", "critical", 9*time.Minute, map[string]string{"instance": "web-02"}),
			alert("DiskFillingUp", "warning", 3*time.Hour, map[string]string{"instance": "db-01"}),
		}),
		msg(resourceAlerts, "staging", []backend.Alert{
			alert("CertExpiringSoon", "info", 26*time.Hour, map[string]string{"instance": "lb-01"}),
		}),
		msg(resourceSilences, "prod", []backend.Silence{
			silence("sil-prod-1", "alice", 2*time.Hour),
			silence("sil-prod-2", "bob", 30*time.Minute),
		}),
		msg(resourceSilences, "staging", []backend.Silence{
			silence("sil-staging-1", "carol", 6*time.Hour),
		}),
		msg(resourceReceivers, "prod", []backend.Receiver{{Name: "pagerduty"}, {Name: "slack-sre"}}),
		msg(resourceReceivers, "staging", []backend.Receiver{{Name: "slack-dev"}}),
		msg(resourceStatus, "prod", status),
		msg(resourceStatus, "staging", status),
	}
}

// frameCase is one golden frame: the page to push on top of the
// boot-assembled App, plus the keys to press once the canned poll
// data has landed (the drill-down pages are only reachable that way).
type frameCase struct {
	name string
	page func(env *pageEnv) app.Page
	keys []tea.KeyPressMsg
}

func frameCases() []frameCase {
	alertsPage := func(env *pageEnv) app.Page { return newAlertsPage(env, "", "") }
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	return []frameCase{
		{name: "alerts", page: alertsPage},
		{name: "groupdetail", page: alertsPage, keys: []tea.KeyPressMsg{enter}},
		{name: "alert", page: alertsPage, keys: []tea.KeyPressMsg{enter, enter}},
		{name: "silences", page: newSilencesPage},
		{name: "silence", page: newSilencesPage, keys: []tea.KeyPressMsg{enter}},
		{name: "receivers", page: newReceiversPage},
		{name: "status", page: newStatusPage},
		{
			name: "tenant",
			page: func(env *pageEnv) app.Page {
				return newTenantPage(env, func(n string) (app.Page, error) { return newTenantConfigPage(env, n) })
			},
		},
	}
}

// TestGoldenFrame pins the assembled frame — top panel, body, and
// footer — for every page at a wide and a narrow size. The narrow
// size covers the top-panel reflow of ADR 0036.
func TestGoldenFrame(t *testing.T) {
	t.Parallel()
	sizes := []struct{ w, h int }{{120, 40}, {80, 24}}
	for _, tc := range frameCases() {
		for _, size := range sizes {
			name := goldenName(tc.name, size.w, size.h)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				assertGolden(t, name, renderFrame(t, tc, size.w, size.h))
			})
		}
	}
}

// renderFrame boots the real startup graph with fake dependencies,
// pushes the case's page, replays the canned poll data, presses the
// case's keys, and returns the rendered frame with ANSI dropped.
// Colors belong to the theme tests; the golden guards layout.
func renderFrame(t *testing.T, tc frameCase, width, height int) string {
	t.Helper()

	deps := testDeps(t)
	deps.Now = func() time.Time { return frameClock }
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) { return frameConfig(), nil }
	deps.BuildClient = func(config.Backend, string, ...factory.Option) (backend.Client, error) {
		return frameClient{fakeStatusBackend: &fakeStatusBackend{version: "0.28.0"}, t: t}, nil
	}

	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	var m tea.Model = res.App()
	m = step(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m = step(t, m, app.PushPage(func() app.Page { return tc.page(res.env) })())
	for _, data := range framePollData() {
		m = step(t, m, data)
	}
	for _, key := range tc.keys {
		m = step(t, m, key)
	}

	a, ok := m.(*app.App)
	require.True(t, ok, "App.Update must keep returning the App model")
	return normalizeFrame(testutil.StripStyle(a.View().Content))
}

// normalizeFrame drops the right-edge padding lipgloss adds to every
// row and terminates the frame with one newline, which is what the
// trailing-whitespace and end-of-file prek hooks would rewrite a
// stored golden into anyway. The cost is that the goldens no longer
// guard padding width; the visible box borders still do.
func normalizeFrame(frame string) string {
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

// step feeds one message through Update and drains the follow-up
// commands so synchronous effects (page pushes, poll fan-out) land
// before the next message. Commands that do not answer within the
// budget are abandoned: a tea.Tick (flash expiry, hint rotation)
// sleeps its whole interval, and the real runtime would deliver it
// long after this frame was drawn. A command that times out for any
// other reason surfaces as a golden mismatch, so read a surprise
// diff as a possible flake before you reach for -update.
func step(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	const (
		maxMsgs = 8
		budget  = 50 * time.Millisecond
	)
	resolve := func(cmd tea.Cmd) tea.Msg {
		if cmd == nil {
			return nil
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- cmd() }()
		select {
		case out := <-done:
			return out
		case <-time.After(budget):
			return nil
		}
	}

	var queue []tea.Msg
	// Batches nest, and Update has no case for a tea.BatchMsg, so a
	// batch handed to it straight would drop every command inside.
	var enqueue func(tea.Msg)
	enqueue = func(out tea.Msg) {
		switch v := out.(type) {
		case nil, tea.QuitMsg:
		case tea.BatchMsg:
			for _, c := range v {
				enqueue(resolve(c))
			}
		default:
			// tea.Sequence wraps its commands in an unexported
			// sequenceMsg that Update has no case for, so passing one
			// on drops every command inside it. Nothing reaches this
			// today; App.replacePage is the one producer.
			require.NotEqual(t, "tea.sequenceMsg", fmt.Sprintf("%T", v),
				"tea.Sequence needs the same flattening tea.Batch gets")
			queue = append(queue, v)
		}
	}

	enqueue(msg)
	for seen := 0; len(queue) > 0 && seen < maxMsgs; seen++ {
		next := queue[0]
		queue = queue[1:]
		updated, cmd := m.Update(next)
		m = updated
		enqueue(resolve(cmd))
	}
	require.Empty(t, queue,
		"the cascade outran maxMsgs — the frame is half-settled, so raise "+
			"the bound rather than blessing the golden with -update")
	return m
}

func goldenName(page string, width, height int) string {
	return fmt.Sprintf("%s_%dx%d", page, width, height)
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "frames", name+".golden")
	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden frame — run go test ./internal/tui/boot -update")
	require.Equal(t, string(want), got)
}

// TestDepsResolved_NowDefaultsToWallClock keeps the frozen clock
// inside the tests. cmd/tui.go leaves Deps.Now unset, so the
// default decides what a real user's AGE column reads.
func TestDepsResolved_NowDefaultsToWallClock(t *testing.T) {
	t.Parallel()
	now := Deps{}.resolved().Now
	require.NotNil(t, now)
	require.WithinDuration(t, time.Now(), now(), time.Minute)
}

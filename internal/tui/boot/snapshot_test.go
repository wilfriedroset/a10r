// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// snapshotBackend answers every list call from canned fixtures, so
// Snapshot renders a populated frame without a network. block, when
// set, holds ListAlerts open until the test closes it, which is how
// the deadline case starves the poll gate.
type snapshotBackend struct {
	*fakeStatusBackend
	block chan struct{}
	err   error
}

func (b snapshotBackend) ListAlerts(ctx context.Context, _ backend.AlertFilter) ([]backend.Alert, error) {
	if b.block != nil {
		select {
		case <-b.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if b.err != nil {
		return nil, b.err
	}
	return []backend.Alert{pagetest.Alert(pagetest.AlertOptions{
		Name: "HighCPU", Severity: "critical", Now: frameClock,
		Age: 12 * time.Minute, Fingerprint: "fp-highcpu",
	})}, nil
}

func (b snapshotBackend) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	if b.err != nil {
		return nil, b.err
	}
	return []backend.Silence{pagetest.Silence(pagetest.SilenceOptions{
		ID: "sil-1", CreatedBy: "alice", Now: frameClock, EndsIn: 2 * time.Hour,
		Comment:  "scheduled maintenance",
		Matchers: []backend.Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
	})}, nil
}

func (b snapshotBackend) ListReceivers(context.Context) ([]backend.Receiver, error) {
	if b.err != nil {
		return nil, b.err
	}
	return []backend.Receiver{{Name: "pagerduty"}}, nil
}

// newSnapshotResult boots the real startup graph against one fake
// backend named "prod".
func newSnapshotResult(t *testing.T, be snapshotBackend) *Result {
	t.Helper()
	deps := testDeps(t)
	// A buffer rather than os.Stderr so the deadline warning is
	// readable from the test that asserts on it.
	deps.Stderr = &bytes.Buffer{}
	deps.Now = func() time.Time { return frameClock }
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am-prod.internal"}}}, nil
	}
	be.fakeStatusBackend = &fakeStatusBackend{version: "0.28.0"}
	deps.BuildClient = func(config.Backend, string, ...factory.Option) (backend.Client, error) {
		return be, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })
	return res
}

// TestSnapshot_RendersRequestedPage is the headline contract: one
// frame of the named page, filled from a real poll, with no
// terminal attached.
func TestSnapshot_RendersRequestedPage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		page string
		want string
	}{
		{page: resourceAlerts, want: "HighCPU"},
		{page: resourceSilences, want: "alice"},
		{page: resourceReceivers, want: "pagerduty"},
		{page: resourceStatus, want: "0.28.0"},
		{page: pageTenant, want: "am-prod.internal"},
	}
	for _, tc := range cases {
		t.Run(tc.page, func(t *testing.T) {
			t.Parallel()
			res := newSnapshotResult(t, snapshotBackend{})
			frame, err := res.Snapshot(t.Context(), SnapshotOptions{
				Page: tc.page, Width: 120, Height: 40,
			})
			require.NoError(t, err)
			require.Contains(t, frame, tc.want)
			require.Contains(t, frame, "<1> prod", "the top panel must be part of the frame")
			require.Contains(t, frame, "<"+tc.page+">", "the footer crumb must be part of the frame")
			require.NotContains(t, frame, "\x1b[", "the default render strips SGR escapes")
			lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
			require.Len(t, lines, 40, "the frame must fill the requested height")
		})
	}
}

// TestSnapshot_ColorKeepsEscapes covers --color: the skin's SGR
// sequences survive into the frame for the screenshot pipeline.
func TestSnapshot_ColorKeepsEscapes(t *testing.T) {
	t.Parallel()
	res := newSnapshotResult(t, snapshotBackend{})
	frame, err := res.Snapshot(t.Context(), SnapshotOptions{
		Page: resourceAlerts, Width: 120, Height: 40, Color: true,
	})
	require.NoError(t, err)
	require.Contains(t, frame, "\x1b[")
}

// TestSnapshot_ZeroOptionsUseTheDefaults pins the fallbacks the cmd
// layer binds its flag defaults to.
func TestSnapshot_ZeroOptionsUseTheDefaults(t *testing.T) {
	t.Parallel()
	res := newSnapshotResult(t, snapshotBackend{})
	frame, err := res.Snapshot(t.Context(), SnapshotOptions{Page: resourceAlerts})
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	require.Len(t, lines, DefaultSnapshotHeight)
	for i, line := range lines {
		require.LessOrEqual(t, len([]rune(line)), DefaultSnapshotWidth, "line %d overflows the default width", i)
	}
}

// TestSnapshot_UnknownPageRejected pins the allow-list. The cmdbar
// resolver also answers prefixes and `:q`; a snapshot must not be
// able to reach the quit command through the page argument.
func TestSnapshot_UnknownPageRejected(t *testing.T) {
	t.Parallel()
	for _, page := range []string{"nope", "q", "quit", "sil"} {
		t.Run(page, func(t *testing.T) {
			t.Parallel()
			res := newSnapshotResult(t, snapshotBackend{})
			_, err := res.Snapshot(t.Context(), SnapshotOptions{
				Page: page, Width: 80, Height: 24,
			})
			require.ErrorIs(t, err, ErrUnknownPage)
		})
	}
}

// TestSnapshot_DeadlineRendersAnyway covers the hung-backend case:
// the wait is a cap, not a requirement, so a backend that never
// answers still produces a frame instead of hanging the command.
func TestSnapshot_DeadlineRendersAnyway(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	res := newSnapshotResult(t, snapshotBackend{block: block})

	start := time.Now()
	frame, err := res.Snapshot(t.Context(), SnapshotOptions{
		Page: resourceAlerts, Width: 120, Height: 40, Wait: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 3*time.Second, "the wait must cap the render")
	require.Contains(t, frame, "loading alerts", "a starved poll still renders the cold-start body")

	// Spec item 9: the partial frame is still the answer, but the
	// operator hears about it on stderr, never on stdout.
	warnings, ok := res.stderr.(*bytes.Buffer)
	require.True(t, ok)
	require.Contains(t, warnings.String(), "prod/alerts",
		"the warning must name the backend and resource that never reported")
	require.NotContains(t, frame, "warning:")
}

// TestSnapshot_SizedEvenWhenPollSettlesImmediately guards the
// window-size race: bubbletea delivers its own initial
// WindowSizeMsg from a goroutine, so a backend that fails on the
// first call can settle the gate and quit the program before that
// message lands. The frame must still be laid out at the asked-for
// size rather than at zero.
func TestSnapshot_SizedEvenWhenPollSettlesImmediately(t *testing.T) {
	t.Parallel()
	res := newSnapshotResult(t, snapshotBackend{err: fmt.Errorf("dial: %w", backend.ErrUnreachable)})

	start := time.Now()
	frame, err := res.Snapshot(t.Context(), SnapshotOptions{
		Page: resourceAlerts, Width: 100, Height: 30, Wait: time.Minute,
	})
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Minute, "an unreachable backend must settle the gate, not burn the wait")
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	require.Len(t, lines, 30, "the frame must fill the requested height")
	for i, line := range lines {
		require.LessOrEqual(t, len([]rune(line)), 100, "line %d overflows the requested width", i)
	}
}

// TestSnapshot_CancelledContextFails pins that Ctrl+C during the
// wait aborts rather than emitting a half-rendered frame.
func TestSnapshot_CancelledContextFails(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	res := newSnapshotResult(t, snapshotBackend{block: block})

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := res.Snapshot(ctx, SnapshotOptions{
		Page: resourceAlerts, Width: 120, Height: 40, Wait: time.Minute,
	})
	require.ErrorIs(t, err, context.Canceled)
}

// TestPollGate pins when the render stops waiting: every poller
// reports once, or a backend declares itself unreachable.
func TestPollGate(t *testing.T) {
	t.Parallel()
	clients := map[string]backend.Client{"prod": &fakeStatusBackend{}, "staging": &fakeStatusBackend{}}
	cfg := &config.Config{Backends: []config.Backend{{Name: "prod"}, {Name: "staging"}}}

	data := func(tenant, resource string) tea.Msg {
		return poll.DataMsg{Tenant: tenant, ResourceLabel: resource}
	}
	down := func(tenant string) tea.Msg {
		return poll.BackendStatusMsg{Tenant: tenant, State: header.ConnUnreachable}
	}

	t.Run("every poller reports", func(t *testing.T) {
		t.Parallel()
		g := newPollGate(cfg, clients)
		for _, tenant := range []string{"prod", "staging"} {
			for _, resource := range []string{resourceAlerts, resourceSilences, resourceReceivers, resourceStatus} {
				requireOpen(t, g)
				g.observe(data(tenant, resource))
			}
		}
		requireClosed(t, g)
	})

	t.Run("an unreachable backend settles its whole tenant", func(t *testing.T) {
		t.Parallel()
		g := newPollGate(cfg, clients)
		g.observe(down("prod"))
		requireOpen(t, g)
		g.observe(down("staging"))
		requireClosed(t, g)
	})

	t.Run("a healthy status message does not settle anything", func(t *testing.T) {
		t.Parallel()
		g := newPollGate(cfg, clients)
		g.observe(poll.BackendStatusMsg{Tenant: "prod", State: header.ConnConnected})
		g.observe(down("staging"))
		requireOpen(t, g)
	})

	t.Run("no backends is already settled", func(t *testing.T) {
		t.Parallel()
		requireClosed(t, newPollGate(&config.Config{}, nil))
	})

	t.Run("a backend without a client is not waited for", func(t *testing.T) {
		t.Parallel()
		g := newPollGate(cfg, map[string]backend.Client{"prod": &fakeStatusBackend{}})
		for _, resource := range []string{resourceAlerts, resourceSilences, resourceReceivers, resourceStatus} {
			g.observe(data("prod", resource))
		}
		requireClosed(t, g)
	})
}

func requireOpen(t *testing.T, g *pollGate) {
	t.Helper()
	select {
	case <-g.ready:
		t.Fatal("the gate opened before every poller reported")
	default:
	}
}

func requireClosed(t *testing.T, g *pollGate) {
	t.Helper()
	select {
	case <-g.ready:
	default:
		t.Fatal("the gate is still waiting after every poller reported")
	}
}

const (
	headlessBoot    = true
	interactiveBoot = false
)

// notifyingResult boots the graph with tui.notify on, so the only
// difference between the two cases is the headless flag.
func notifyingResult(t *testing.T, headless bool) *Result {
	t.Helper()
	deps := testDeps(t)
	deps.Headless = headless
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{
			Backends: []config.Backend{{Name: "prod", URL: "https://am-prod.internal"}},
			TUI:      config.TUI{Notify: config.Notify{Enabled: true}},
		}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })
	return res
}

func firingPoll(alertname string) poll.DataMsg {
	return poll.DataMsg{
		Tenant:        "prod",
		ResourceLabel: resourceAlerts,
		Resource: []backend.Alert{{
			Labels: map[string]string{"alertname": alertname, "severity": "critical"},
			State:  backend.AlertStateActive,
		}},
	}
}

// notifyFlashed reports whether the App raised the notifier's flash
// while handling msg. The flash is the one notifier effect a test can
// read without a terminal; the bell and the subprocess ride the same
// tea.Cmd batch.
func notifyFlashed(a *app.App, msg tea.Msg) bool {
	_, cmd := a.Update(msg)
	for _, m := range drain(cmd) {
		if _, ok := m.(footer.FlashShowMsg); ok {
			return true
		}
	}
	return false
}

func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, drain(c)...)
	}
	return out
}

// TestBuild_HeadlessSkipsTheNotifier pins spec 15 item 12: a
// headless command never notifies, whatever the config says. A
// screenshot run on a machine with tui.notify on must not ring the
// bell or spawn the configured program.
func TestBuild_HeadlessSkipsTheNotifier(t *testing.T) {
	t.Parallel()
	a := notifyingResult(t, headlessBoot).App()

	require.False(t, notifyFlashed(a, firingPoll("HighCPU")), "the first poll only warms up")
	require.False(t, notifyFlashed(a, firingPoll("DiskFull")),
		"a headless render must stay silent on a new firing alert")
}

// TestBuild_InteractiveKeepsTheNotifier is the companion: the same
// config on the TUI path still notifies, so the rule is the boot
// path rather than the config.
func TestBuild_InteractiveKeepsTheNotifier(t *testing.T) {
	t.Parallel()
	a := notifyingResult(t, interactiveBoot).App()

	require.False(t, notifyFlashed(a, firingPoll("HighCPU")), "the first poll only warms up")
	require.True(t, notifyFlashed(a, firingPoll("DiskFull")),
		"the interactive path must still announce a new firing alert")
}

// TestSnapshot_NamesABackendWithNoClient pins the frame a
// misconfigured backend produces. Such a backend has no poller, so
// nothing else can put it in the band.
func TestSnapshot_NamesABackendWithNoClient(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	deps := testDeps(t)
	deps.Stderr = &stderr
	deps.Now = func() time.Time { return frameClock }
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{
			{Name: "prod", URL: "https://am-prod.internal"},
			{Name: "broken", URL: "https://am-broken.internal"},
		}}, nil
	}
	deps.BuildClient = func(cfg config.Backend, _ string, _ ...factory.Option) (backend.Client, error) {
		if cfg.Name == "broken" {
			return nil, errors.New("bad tls bundle")
		}
		return snapshotBackend{fakeStatusBackend: &fakeStatusBackend{version: "0.28.0"}}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	frame, err := res.Snapshot(t.Context(), SnapshotOptions{
		Page: resourceAlerts, Width: 120, Height: 40, Wait: 5 * time.Second,
	})
	require.NoError(t, err)
	require.Contains(t, frame, "broken: client build failed",
		"the frame must name the tenant it could not reach")

	require.Contains(t, stderr.String(), `no client for "broken"`,
		"a pipeline reading stderr and a human reading the frame must agree")
	require.Contains(t, stderr.String(), "bad tls bundle")
}

// TestWaitOr covers the fallback the cmd layer depends on: the
// snapshot command has no wait flag, so every run arrives here with
// a zero.
func TestWaitOr(t *testing.T) {
	t.Parallel()
	require.Equal(t, DefaultSnapshotWait, waitOr(0))
	require.Equal(t, DefaultSnapshotWait, waitOr(-time.Second))
	require.Equal(t, 50*time.Millisecond, waitOr(50*time.Millisecond))
}

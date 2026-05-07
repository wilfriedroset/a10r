// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"log/slog"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/page/alerts"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// fuzzNow / fuzzAlerts are hoisted to package scope so the per-
// iteration boot path doesn't reallocate the alert slice on every
// fuzz exec. Read-only from the fuzz fn; safe to share.
var (
	fuzzNow    = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	fuzzAlerts = []backend.Alert{
		{
			Labels:   map[string]string{"alertname": "HighCPU", "severity": "critical"},
			State:    backend.AlertStateActive,
			StartsAt: fuzzNow.Add(-time.Minute),
		},
		{
			Labels:   map[string]string{"alertname": "LowDisk", "severity": "warning"},
			State:    backend.AlertStateActive,
			StartsAt: fuzzNow.Add(-time.Minute),
		},
	}
)

// FuzzApp is the top-level fuzz target. Each iteration builds a
// fresh App with the alerts page pushed and one synthetic
// poll.DataMsg hydrated, then drives a decoded msg stream through
// Update + View. Oracle is panic-only — see docs/design/fuzzing.md.
func FuzzApp(f *testing.F) {
	addAppSeeds(f)

	f.Fuzz(func(t *testing.T, in []byte) {
		// Bound the per-iteration cost. Long fuzz inputs produce
		// many frames; processing all of them in one iteration
		// stalls the fuzz scheduler on a single worker. 64 msgs
		// is enough to reach any modal / form state and keeps
		// iters short enough for the fuzzer to bisect.
		const maxFrames = 64
		msgs := testutil.DecodeFuzzMsgs(in)
		if len(msgs) > maxFrames {
			msgs = msgs[:maxFrames]
		}
		m := bootApp(t)
		for _, msg := range msgs {
			m = step(m, msg)
		}
	})
}

// step feeds one message through Update, resolves the returned
// Cmd shallowly so synchronous follow-up messages (PushPage,
// OpenModal, ScopeChanged, …) actually land, then calls View()
// once to surface render-path panics. Cmd resolution is bounded
// so a runaway batch cascade can't stall a fuzz iteration; depth
// 8 covers the deepest legit chain we know of (silence-form
// submit → CreateSilence → ScopeChanged → poll refresh fan-out).
// View runs once per outer step rather than per cmd-resolution
// depth because lipgloss layout dominates per-iteration cost;
// transient states get rendered indirectly on the next step.
func step(m tea.Model, msg tea.Msg) tea.Model {
	const maxDepth = 8
	queue := []tea.Msg{msg}
	depth := 0
	for len(queue) > 0 && depth < maxDepth {
		depth++
		next := queue[0]
		queue = queue[1:]
		updated, cmd := m.Update(next)
		m = updated
		if cmd == nil {
			continue
		}
		out := cmd()
		switch v := out.(type) {
		case nil:
			// no follow-up
		case tea.BatchMsg:
			for _, c := range v {
				if c == nil {
					continue
				}
				if r := c(); r != nil {
					queue = append(queue, r)
				}
			}
		case tea.QuitMsg:
			// Would terminate the program; for fuzz we just stop
			// resolving so we don't loop on a quit rebroadcast.
			return m
		default:
			queue = append(queue, v)
		}
	}
	_ = m.View()
	return m
}

// bootApp constructs the App, pushes the alerts home page, and
// hydrates it with one synthetic poll.DataMsg so the fuzzer's
// random keys land on a populated table from the first iteration.
func bootApp(t *testing.T) tea.Model {
	t.Helper()
	styles := testutil.LoadFuzzStyles(t)
	a := app.NewApp(app.Options{
		Styles:     styles,
		Registry:   action.New(),
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
	})

	clients := map[string]silenceform.Client{
		"prod":    &testutil.FakeSilenceClient{},
		"staging": &testutil.FakeSilenceClient{},
	}
	homeFactory := func() app.Page {
		return alerts.New(alerts.Options{
			Styles:          styles,
			Now:             func() time.Time { return fuzzNow },
			Scope:           "all",
			Clients:         clients,
			Creator:         "fuzz",
			BulkConcurrency: 4,
			Logger:          slog.Default(),
		})
	}

	var m tea.Model = a
	m = step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = step(m, app.PushPage(homeFactory)())
	m = step(m, poll.DataMsg{
		Resource:      fuzzAlerts,
		Tenant:        "prod",
		ResourceLabel: "alerts",
		At:            fuzzNow,
	})
	return m
}

// addAppSeeds registers a corpus that drives the app into
// distinct pre-fuzz states per seed. Mutated bytes start
// exploring from those states immediately rather than mashing
// keys on the home page.
func addAppSeeds(f *testing.F) {
	f.Helper()

	// Empty input — exercises the boot path only.
	f.Add([]byte{})

	// Resize extremes (idx*4 → 0, 4, 84, 252).
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(0, 0)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(1, 1)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(63, 63)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameResize(20, 6)))

	// Vim navigation on the alerts list.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('j'), testutil.FuzzFrameKey('j'),
		testutil.FuzzFrameKey('k'), testutil.FuzzFrameKey('G'),
		testutil.FuzzFrameKey('g'), testutil.FuzzFrameKey('g'),
	))

	// Modal cycles — open and close help / cmdbar.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey('?'), testutil.FuzzFrameKeyCode(tea.KeyEscape)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey(':'), testutil.FuzzFrameKeyCode(tea.KeyEscape)))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey(':'), testutil.FuzzFrameKey('a'), testutil.FuzzFrameKeyCode(tea.KeyEnter)))

	// Tenant quick-switch.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey('1'), testutil.FuzzFrameKey('2'), testutil.FuzzFrameKey('0')))

	// Filter prompt.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('/'), testutil.FuzzFrameKey('h'), testutil.FuzzFrameKey('i'),
		testutil.FuzzFrameKeyCode(tea.KeyEnter),
	))
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey('/'), testutil.FuzzFrameKeyCode(tea.KeyEscape)))

	// Silence flow on the cursor row — `s` opens the form, then
	// random follow-up bytes should not crash the form push.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey('s')))
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('s'),
		testutil.FuzzFrameKey('1'), testutil.FuzzFrameKey('h'),
		testutil.FuzzFrameKeyCode(tea.KeyEnter),
	))

	// Drill into detail then back out.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKeyCode(tea.KeyEnter), testutil.FuzzFrameKeyCode(tea.KeyEscape)))

	// Time-format and refresh toggles.
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKey('t'), testutil.FuzzFrameKey('r')))

	// Tenant picker open/close (Ctrl+T).
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKeyCtrl('t'), testutil.FuzzFrameKeyCode(tea.KeyEscape)))
}

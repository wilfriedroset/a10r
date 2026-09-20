// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/notify"
	"github.com/wilfriedroset/a10r/internal/tui/page/alerts"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// fuzzNow / fuzzAlerts are hoisted to package scope so the per-
// iteration boot path doesn't reallocate the alert slice on every
// fuzz exec. Read-only from the fuzz fn; safe to share.
var (
	// fuzzRules is hoisted for the reason the slices below are: the
	// guarded target rebuilds nothing per exec. Read-only from the
	// fuzz fn.
	fuzzRules = guardrail.Set{
		{Tenants: []string{"staging"}, Deny: true, Reason: "use the change ticket"},
		{Tenants: []string{"prod"}, Confirmation: guardrail.ConfirmationTypeTenantName},
	}
	fuzzNow    = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	fuzzAlerts = []backend.Alert{
		{
			Labels:   map[string]string{"alertname": "HighCPU", "severity": "critical", "cluster": "eu-1"},
			State:    backend.AlertStateActive,
			StartsAt: fuzzNow.Add(-time.Minute),
		},
		// Same aggregate as the row above with a different cluster, so
		// the label-column rollup renders its `<N values>` marker and
		// the marker comparator gets fuzzed alongside the plain cells.
		{
			Labels:   map[string]string{"alertname": "HighCPU", "severity": "warning", "cluster": "us-1"},
			State:    backend.AlertStateSuppressed,
			StartsAt: fuzzNow.Add(-2 * time.Minute),
		},
		// No cluster label at all, so the empty-cell tail-sort path is
		// reachable too.
		{
			Labels:   map[string]string{"alertname": "LowDisk", "severity": "warning"},
			State:    backend.AlertStateActive,
			StartsAt: fuzzNow.Add(-time.Minute),
		},
	}

	// fuzzNotifyAlerts carries every row of fuzzAlerts plus one alert
	// whose name holds a `;` and an ESC. A poll of it after the warm-up
	// poll is what makes the notifier diff, sanitise and emit, so the
	// hostile name reaches tea.Raw on the fuzzed path.
	fuzzNotifyAlerts = append(slices.Clone(fuzzAlerts), backend.Alert{
		Labels:   map[string]string{"alertname": "Evil;\x1b]9;own", "severity": "critical"},
		State:    backend.AlertStateActive,
		StartsAt: fuzzNow.Add(-time.Minute),
	})

	// fuzzColumns gives the fuzzer a user-declared column with a sort
	// key, so Shift+L walks into a comparator the built-in set does
	// not own. The second column is wide and also a sort axis, so
	// Shift+W drives the tier toggle and the parked-sort path.
	fuzzColumns = []config.Column{
		{Label: "cluster", SortKey: "L"},
		{Label: "team", Title: "OWNER", Width: 8, Wide: true, SortKey: "O"},
	}
)

// FuzzApp is the top-level fuzz target. Each iteration builds a
// fresh App with the alerts page pushed and one synthetic
// poll.DataMsg hydrated, then drives a decoded msg stream through
// Update + View. Oracle is panic-only: any panic from Update or
// View on any synthesised input fails the iteration.
func FuzzApp(f *testing.F) {
	addAppSeeds(f)

	f.Fuzz(func(t *testing.T, in []byte) {
		driveFrames(t, in, nil)
	})
}

// FuzzGuardedApp is FuzzApp under a write policy: staging is denied
// and prod asks for its name to be typed. Both tenants carry rows, so
// the policy adds two states no other seed reaches, a key that refuses
// and a prompt only an exact name clears, and both sit on the write
// path.
func FuzzGuardedApp(f *testing.F) {
	addGuardedSeeds(f)

	f.Fuzz(func(t *testing.T, in []byte) {
		driveFrames(t, in, fuzzRules)
	})
}

// driveFrames runs one fuzz iteration against an app booted under the
// given policy.
func driveFrames(t *testing.T, in []byte, rules guardrail.Set) {
	t.Helper()
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
	m := bootApp(t, rules)
	for _, msg := range msgs {
		m = step(m, msg)
	}
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
		out := resolveCmd(cmd)
		switch v := out.(type) {
		case nil:
			// no follow-up
		case tea.BatchMsg:
			for _, c := range v {
				if r := resolveCmd(c); r != nil {
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

// cmdBudget bounds how long resolveCmd waits for a Cmd to produce its
// message. Synchronous follow-ups (PushPage, OpenModal, ScopeChanged)
// return in microseconds; this leaves a wide margin over that even on a
// loaded CI runner.
const cmdBudget = 50 * time.Millisecond

// resolveCmd runs a Cmd and returns its message, abandoning it if it
// blocks past cmdBudget. Bubble Tea runs Cmds on their own goroutines,
// so a tea.Tick (flash auto-clear, hint rotation, chord expiry) sleeps
// its full interval inside cmd() -- seconds -- which would stall the
// step loop and make the fuzzer flag the seed as hung. Dropping the
// late message mirrors the real runtime, where the tick fires long
// after this iteration. The buffered channel lets the abandoned
// goroutine send and exit rather than leak.
func resolveCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(cmdBudget):
		return nil
	}
}

// fuzzSkins memoises the compiled skins across iterations, because
// the fuzz target re-boots the app per input and every `:skin` seed
// would otherwise pay a YAML parse and a full compile. The cached
// value is only ever read: applySkin copies out of it.
var fuzzSkins sync.Map

func fuzzLoadSkin(name string) (*theme.Styles, error) {
	if cached, ok := fuzzSkins.Load(name); ok {
		styles, _ := cached.(*theme.Styles)
		return styles, nil
	}
	styles, err := (&theme.Loader{}).Load(name)
	if err != nil {
		return nil, err
	}
	fuzzSkins.Store(name, styles)
	return styles, nil
}

// bootApp constructs the App, pushes the alerts home page, and
// hydrates it with one synthetic poll.DataMsg so the fuzzer's
// random keys land on a populated table from the first iteration.
func bootApp(t *testing.T, rules guardrail.Set) tea.Model {
	t.Helper()
	// Copied, not shared: LoadFuzzStyles caches one pointer for the
	// whole test binary, and applySkin swaps by writing through
	// Options.Styles.
	base := *testutil.LoadFuzzStyles(t)
	styles := &base
	// `:skin` is wired so the seeds below reach the picker's render
	// and submit paths. The resolver is local rather than boot's: the
	// fuzz app has no config to build the real one from.
	resolver := cmdbar.New()
	resolver.Register("skin", func(args []string) tea.Cmd {
		if len(args) > 0 {
			return app.ApplySkin(args[0])
		}
		return app.OpenSkinPicker()
	})
	resolver.Register("reload", func([]string) tea.Cmd { return app.Reload() })
	a := app.NewApp(app.Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		Tenants:    []string{"prod", "staging"},
		CmdBar:     resolver,
		SkinNames:  func() []string { return theme.Names("") },
		SkinName:   theme.DefaultSkinName,
		LoadStyles: fuzzLoadSkin,
		// A reload that always succeeds and always repaints, so the
		// seed below drives applyReloaded's skin swap and hint-bar
		// rebuild rather than its refusal path.
		Reload: func() tea.Cmd {
			return func() tea.Msg {
				return app.ReloadedMsg{ThemeName: "catppuccin-latte", Tips: true, TipsInterval: time.Second}
			}
		},
		// Notifications on with both escape transports. The two
		// warm-up polls below only seed the firing set; the third one
		// carries a new alertname and is what drives the diff, the
		// sanitiser and the raw emission.
		Notify: notify.New(config.Notify{Enabled: true, Desktop: config.NotifyDesktopBoth}),
	})

	clients := map[string]silenceform.Client{
		"prod":    &testutil.FakeSilenceClient{},
		"staging": &testutil.FakeSilenceClient{},
	}
	homeFactory := func() app.Page {
		return alerts.New(alerts.Options{
			Styles:             styles,
			Now:                func() time.Time { return fuzzNow },
			Scope:              "all",
			Clients:            clients,
			Creator:            "fuzz",
			BulkConcurrency:    4,
			Logger:             slog.Default(),
			Columns:            fuzzColumns,
			GroupDetailColumns: fuzzColumns,
			Guardrails:         rules,
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
	// A second tenant carries rows as well, so a per-tenant policy and
	// the cross-tenant rollup both have something to act on.
	m = step(m, poll.DataMsg{
		Resource:      fuzzAlerts,
		Tenant:        "staging",
		ResourceLabel: "alerts",
		At:            fuzzNow,
	})
	// prod polls a second time with one alertname the poll before it
	// did not have, which is the only thing that makes the notifier
	// speak.
	m = step(m, poll.DataMsg{
		Resource:      fuzzNotifyAlerts,
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

	// `:skin` both ways: the bare verb opens the picker (Esc closes
	// it), and a named skin repaints every frame that follows.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey(':'), testutil.FuzzFrameKey('s'), testutil.FuzzFrameKey('k'),
		testutil.FuzzFrameKeyCode(tea.KeyEnter), testutil.FuzzFrameKeyCode(tea.KeyEscape),
	))
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey(':'), testutil.FuzzFrameKey('s'), testutil.FuzzFrameKey('k'),
		testutil.FuzzFrameKeyCode(tea.KeyEnter),
		testutil.FuzzFrameKey('l'), testutil.FuzzFrameKey('a'),
		testutil.FuzzFrameKeyCode(tea.KeyEnter),
	))

	// `:reload` repaints and rebuilds the hint bar mid-session, so
	// the frames after it exercise a swapped skin under a live stack.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey(':'), testutil.FuzzFrameKey('r'), testutil.FuzzFrameKey('e'),
		testutil.FuzzFrameKey('l'), testutil.FuzzFrameKeyCode(tea.KeyEnter),
		testutil.FuzzFrameResize(80, 24),
	))

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

	// Range marking: anchor, walk, commit, then a bulk verb on the
	// resulting marks.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('V'), testutil.FuzzFrameKey('j'), testutil.FuzzFrameKey('j'),
		testutil.FuzzFrameKeyCode(tea.KeySpace), testutil.FuzzFrameKey('s'),
	))
	// An anchored range interrupted by Esc, then a second Esc that
	// must reach the stack now that the range is gone.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('V'), testutil.FuzzFrameKeyCode(tea.KeyEscape),
		testutil.FuzzFrameKeyCode(tea.KeyEscape),
	))
	// A filter change mid-preview, which is what drops the anchor row.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('V'), testutil.FuzzFrameKey('j'),
		testutil.FuzzFrameKey('/'), testutil.FuzzFrameKey('z'), testutil.FuzzFrameKeyCode(tea.KeyEnter),
		testutil.FuzzFrameKeyCode(tea.KeySpace),
	))

	// Tenant picker open/close (Ctrl+T).
	f.Add(testutil.FuzzSeed(testutil.FuzzFrameKeyCtrl('t'), testutil.FuzzFrameKeyCode(tea.KeyEscape)))

	// Scope narrowing and widening, which drops the notifier's
	// per-tenant firing set. The codec cannot synthesise a poll, so
	// the re-warm half is out of the fuzzer's reach.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('1'), testutil.FuzzFrameKey('r'),
		testutil.FuzzFrameKey('0'), testutil.FuzzFrameKey('r'),
	))

	// Wide tier: sort on the wide column, hide it so the sort parks,
	// then walk the axes with h/l while it is out of view. h/l rather
	// than Left/Right because the frame codec carries printable ASCII
	// and the two pairs reach the same sorter primitives.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('W'), testutil.FuzzFrameKey('O'), testutil.FuzzFrameKey('W'),
		testutil.FuzzFrameKey('h'), testutil.FuzzFrameKey('l'),
		testutil.FuzzFrameKey('W'),
	))
}

// addGuardedSeeds drives the shapes a policy adds: a typed prompt
// answered right and wrong, a key the policy refuses, and the two
// overlays that render the policy.
func addGuardedSeeds(f *testing.F) {
	f.Helper()

	f.Add([]byte{})
	// Mark a row and silence it, which is the write the prod rule
	// guards, then type the tenant name and submit.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKeyCode(tea.KeySpace), testutil.FuzzFrameKey('s'),
		testutil.FuzzFrameKey('p'), testutil.FuzzFrameKey('r'), testutil.FuzzFrameKey('o'),
		testutil.FuzzFrameKey('d'), testutil.FuzzFrameKeyCode(tea.KeyEnter),
	))
	// The same prompt answered with a name that does not match, then
	// Enter, which must leave the prompt open rather than write.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKeyCode(tea.KeySpace), testutil.FuzzFrameKey('s'),
		testutil.FuzzFrameKey('n'), testutil.FuzzFrameKey('o'),
		testutil.FuzzFrameKeyCode(tea.KeyEnter), testutil.FuzzFrameKeyCode(tea.KeyEscape),
	))
	// A row on the denied tenant, where the key refuses instead of
	// opening anything, then the same key after a filter reshuffles
	// which row the cursor sits on.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('j'), testutil.FuzzFrameKey('s'),
		testutil.FuzzFrameKey('/'), testutil.FuzzFrameKey('z'), testutil.FuzzFrameKeyCode(tea.KeyEnter),
		testutil.FuzzFrameKey('s'),
	))

	// The help overlay and the tenant picker under the policy, which
	// render the guarded and denied rows.
	f.Add(testutil.FuzzSeed(
		testutil.FuzzFrameKey('?'), testutil.FuzzFrameKeyCode(tea.KeyEscape),
		testutil.FuzzFrameKeyCtrl('t'), testutil.FuzzFrameKeyCode(tea.KeyEscape),
	))
}

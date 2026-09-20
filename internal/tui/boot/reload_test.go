// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/backendtest"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// The whole safety story of `:reload` rests on this predicate: a
// session keeps its clients, its pollers and its audited log sink,
// so anything those were built from has to refuse the reload rather
// than drift out of sync with the config the user is now reading.
func TestFrozenConfigChanged(t *testing.T) {
	t.Parallel()

	base := func() *config.Config {
		return &config.Config{
			Backends: []config.Backend{{
				Name:        "prod",
				URL:         "https://am.example.com",
				BearerToken: "t",
				Headers:     map[string]string{"X-Team": "sre"},
			}},
			Log: config.Log{Path: "/tmp/a10r.log", Level: "info"},
		}
	}

	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   bool
	}{
		{name: "untouched", mutate: func(*config.Config) {}},
		{name: "read_only flips live", mutate: func(c *config.Config) { c.Backends[0].ReadOnly = true }},
		{name: "poll_interval changes live", mutate: func(c *config.Config) { c.Backends[0].PollInterval = time.Minute }},
		{name: "theme is not a frozen field", mutate: func(c *config.Config) { c.Theme.Name = "nord" }},

		{name: "url", mutate: func(c *config.Config) { c.Backends[0].URL = "https://other" }, want: true},
		{name: "credential", mutate: func(c *config.Config) { c.Backends[0].BearerToken = "u" }, want: true},
		{name: "injected header", mutate: func(c *config.Config) { c.Backends[0].Headers["X-Team"] = "db" }, want: true},
		{name: "rename", mutate: func(c *config.Config) { c.Backends[0].Name = "prod2" }, want: true},
		{name: "backend added", mutate: func(c *config.Config) {
			c.Backends = append(c.Backends, config.Backend{Name: "eu", URL: "https://eu"})
		}, want: true},
		{name: "backend removed", mutate: func(c *config.Config) { c.Backends = nil }, want: true},
		{name: "log path", mutate: func(c *config.Config) { c.Log.Path = "/tmp/other.log" }, want: true},
		{name: "log level", mutate: func(c *config.Config) { c.Log.Level = "debug" }, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			old, next := base(), base()
			tc.mutate(next)
			require.Equal(t, tc.want, frozenConfigChanged(old, next))
		})
	}
}

// A field added to config.Backend later has no reload story yet, so
// the default has to be "frozen": refusing a reload costs a restart,
// while applying a field the live clients were built from leaves the
// session lying about what it is talking to.
func TestFrozenBackend_ClearsOnlyTheReloadableFields(t *testing.T) {
	t.Parallel()

	be := config.Backend{Name: "prod", URL: "https://am", ReadOnly: true, PollInterval: time.Minute}

	require.Equal(t,
		config.Backend{Name: "prod", URL: "https://am"},
		frozenBackend(be),
	)
}

// A changed poll interval only reaches the wire if the pollers are
// rebuilt: poll.Poller reads its interval once, at construction.
func TestStartBackendPoller_RestartRebuildsUnderTheNewInterval(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am", PollInterval: time.Hour}}}
	clients := map[string]backend.Client{"prod": &backendtest.ClientStub{}}
	reg := &pollerRegistry{}

	stop := startBackendPoller(t.Context(), cfg, clients, func(tea.Msg) {}, reg)
	t.Cleanup(stop)
	before := reg.count()

	next := &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am", PollInterval: time.Minute}}}
	reg.Restart(next)

	require.Equal(t, before, reg.count(), "a restart replaces the poller set, it does not grow it")
	require.NotZero(t, before, "the fixture must build pollers or the assertion above is vacuous")
}

// Restart before StartPollers has run would otherwise panic on a nil
// closure. Build returns before cmd/tui.go starts the pollers, so the
// window is real, and nothing to restart is not an error.
func TestPollerRegistry_RestartBeforeStartIsANoOp(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() { (&pollerRegistry{}).Restart(&config.Config{}) })
}

// reloadFixture wires a reloader over a stub loader, so a test says
// "the file now reads like this" without touching a disk.
func reloadFixture(t *testing.T, start config.Config, next func() (*config.Config, error)) (r *reloader, env *pageEnv, live config.Config) {
	t.Helper()
	// Resolved, because that is what Build puts behind env.Config. A
	// raw start config would differ from every reloaded one in the
	// defaults the resolver fills in, and the frozen check would
	// refuse reloads production accepts.
	effective, err := resolveEffectiveConfig(&config.CLIFlags{}, &start)
	require.NoError(t, err)
	cfg := effective.Config
	env = &pageEnv{Config: &cfg}
	d := testDeps(t).resolved()
	d.LoadConfig = func(config.LoadOpts) (*config.Config, error) { return next() }
	return &reloader{
		deps:       d,
		flags:      &config.CLIFlags{},
		env:        env,
		registry:   &pollerRegistry{},
		resolver:   newResolver(env),
		dispatcher: buildDispatcher(),
	}, env, effective.Config
}

// A config that no longer parses is the common reload failure: the
// user is mid-edit. Keeping every live value is the only answer that
// does not punish them for pressing the key one keystroke early.
func TestReload_AParseErrorChangesNothing(t *testing.T) {
	t.Parallel()

	start := config.Config{Theme: config.Theme{Name: "nord"}}
	r, env, live := reloadFixture(t, start, func() (*config.Config, error) {
		return nil, errors.New("yaml: line 4: did not find expected key")
	})

	cmd := r.reload()

	require.Equal(t, live, *env.Config, "a failed reload must leave the live config alone")
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "did not find expected key")
}

// The clients, the pollers and the log sink outlive a reload, so a
// config that no longer describes them has to be refused whole
// rather than applied in part.
func TestReload_RefusesAChangedBackend(t *testing.T) {
	t.Parallel()

	start := config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
	r, env, live := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://elsewhere"}}}, nil
	})

	cmd := r.reload()

	require.Equal(t, live, *env.Config)
	require.Equal(t,
		footer.FlashShowMsg{Level: footer.FlashWarn, Text: "reload: backends or log changed, restart a10r"},
		cmd(),
	)
}

// The point of the verb: the values a new page reads come from the
// file the user edited, without a restart.
func TestReload_AppliesTheReloadableSubset(t *testing.T) {
	t.Parallel()

	start := config.Config{Theme: config.Theme{Name: "nord"}}
	r, env, _ := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{
			Theme: config.Theme{Name: "catppuccin-latte"},
			TUI:   config.TUI{Tips: true, TipsInterval: 30 * time.Second},
		}, nil
	})

	cmd := r.reload()

	require.Equal(t, "catppuccin-latte", env.Config.Theme.Name, "a page pushed after the reload must read the new config")
	require.Equal(t,
		app.ReloadedMsg{ThemeName: "catppuccin-latte", Tips: true, TipsInterval: 30 * time.Second},
		cmd(),
	)
}

// read_only is on the reloadable list, and it sits inside a Backend
// next to the frozen fields, so the frozen check has to let it
// through rather than refuse the whole reload.
func TestReload_AcceptsAPerBackendReadOnlyFlip(t *testing.T) {
	t.Parallel()

	start := config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
	r, env, _ := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am", ReadOnly: true}}}, nil
	})

	require.IsType(t, app.ReloadedMsg{}, r.reload()())
	require.True(t, env.Config.Backends[0].ReadOnly)
}

// Step 8 of the spec: a reload re-runs the alias loader, so an alias
// added to the file works without a restart and one deleted from it
// stops working.
func TestReload_ReRegistersUserAliases(t *testing.T) {
	t.Parallel()

	r, _, _ := reloadFixture(t, config.Config{}, func() (*config.Config, error) {
		return &config.Config{}, nil
	})
	r.deps.LoadAliases = func(string) (config.AliasMap, error) {
		return config.AliasMap{"eu": "tenant eu"}, nil
	}
	require.NoError(t, r.resolver.ReplaceUser(map[string]string{"prod": "tenant prod"}))

	require.IsType(t, app.ReloadedMsg{}, r.reload()())

	require.Equal(t,
		[]cmdbar.UserAlias{{Short: "eu", Expanded: "tenant eu"}},
		r.resolver.UserAliases(),
	)
}

// An alias file that no longer loads keeps the whole session as it
// was, config included: the reload is refused, not half-applied.
func TestReload_AnAliasErrorAppliesNothing(t *testing.T) {
	t.Parallel()

	start := config.Config{Theme: config.Theme{Name: "nord"}}
	r, env, live := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{Theme: config.Theme{Name: "catppuccin-latte"}}, nil
	})
	r.deps.LoadAliases = func(string) (config.AliasMap, error) {
		return nil, errors.New("aliases.yaml: line 2: bad indent")
	}

	cmd := r.reload()

	require.Equal(t, live, *env.Config, "the config must not move when a later step fails")
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Contains(t, msg.Text, "bad indent")
}

// Same contract for the keys file: an unknown action name keeps the
// keys the session already dispatches.
func TestReload_AKeysErrorAppliesNothing(t *testing.T) {
	t.Parallel()

	start := config.Config{Theme: config.Theme{Name: "nord"}}
	r, env, live := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{Theme: config.Theme{Name: "catppuccin-latte"}}, nil
	})
	r.deps.LoadKeys = func(string, string) (config.KeyOverrides, error) {
		return config.KeyOverrides{"nosuchaction": {"X"}}, nil
	}

	cmd := r.reload()

	require.Equal(t, live, *env.Config)
	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Contains(t, msg.Text, "nosuchaction")
}

// Options.Reload is what separates a working verb from one that
// flashes "reload is unavailable", and only Build can wire it.
func TestBuild_WiresReloadIntoTheApp(t *testing.T) {
	t.Parallel()

	res, err := Build(t.Context(), &config.CLIFlags{}, testDeps(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Close() })

	_, cmd := res.App().Update(app.ReloadRequestedMsg{})

	require.NotNil(t, cmd)
	require.IsType(t, app.ReloadedMsg{}, cmd())
}

// The tick a poller runs on is the effective one: a per-page
// override beats the per-backend value, which beats the global
// default. Comparing only the per-backend field would report "no
// change" for the two settings most users actually edit.
func TestReload_RestartsPollersOnEveryIntervalLayer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   bool
	}{
		{name: "nothing moved", mutate: func(*config.Config) {}},
		// Not time.Minute: that is what an unset interval already
		// falls back to, so the case would pass without moving a tick.
		{name: "per-backend", mutate: func(c *config.Config) { c.Backends[0].PollInterval = 30 * time.Second }, want: true},
		{name: "global default", mutate: func(c *config.Config) { c.Defaults.PollInterval = 30 * time.Second }, want: true},
		{name: "per-page override", mutate: func(c *config.Config) { c.Pages.Alerts.PollInterval = 30 * time.Second }, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			old := &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
			next := &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
			tc.mutate(next)
			require.Equal(t, tc.want, pollIntervalsChanged(old, next))
		})
	}
}

// defaults.log_format is what the live slog handler was built with,
// so it belongs on the frozen side even though it does not sit under
// `log:`. A session that accepted it would report one format and
// write another.
func TestFrozenConfigChanged_CoversTheLogFormat(t *testing.T) {
	t.Parallel()

	old := &config.Config{}
	next := &config.Config{Defaults: config.Defaults{LogFormat: "json"}}

	require.True(t, frozenConfigChanged(old, next))
}

// A user who just tightened read_only and saw a plain "reloaded" has
// fair grounds to think the running page is covered. It is not, until
// the live propagation lands, so the flash has to say which pages the
// new value reaches.
func TestReload_SaysWhereReadOnlyApplies(t *testing.T) {
	t.Parallel()

	r, _, _ := reloadFixture(t, config.Config{}, func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{ReadOnly: true}}, nil
	})

	msg, ok := r.reload()().(app.ReloadedMsg)

	require.True(t, ok)
	require.True(t, msg.ReadOnlyChanged)
	require.True(t, msg.ReadOnly, "the App composes its help overlay from this value")
}

// The pollers must run on the config the rest of the session reads,
// or the first `:reload` silently re-ticks every backend under
// values the boot never used.
func TestStartPollers_UsesTheEffectiveConfig(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}, nil
	}

	res, err := Build(t.Context(), &config.CLIFlags{PollInterval: 30 * time.Second}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Close() })

	require.Same(t, res.env.Config, res.cfg, "the pollers and the pages must read one config")
}

// `:info` prints the alias count, and `:reload` is the one thing
// that moves it mid-session. A count captured at boot contradicts
// the aliases the command bar now resolves.
func TestBuildInfoReport_FollowsAReloadedAliasCount(t *testing.T) {
	t.Parallel()

	env := &pageEnv{Config: &config.Config{}}
	resolver := newResolver(env)
	render := buildInfoReport(infoInputs{
		deps:       testDeps(t).resolved(),
		cfg:        env.Config,
		aliasCount: func() int { return len(resolver.UserAliases()) },
	})
	require.Zero(t, render().AliasCount)

	require.NoError(t, resolver.ReplaceUser(map[string]string{"eu": "tenant eu"}))

	require.Equal(t, 1, render().AliasCount)
}

// The predicate and the registry are each covered on their own; this
// pins the wire between them. A reload that decided the tick moved
// and then never told the registry would flash success and keep
// polling on the old interval for the rest of the session.
func TestReload_HandsTheNewConfigToTheRegistry(t *testing.T) {
	t.Parallel()

	start := config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
	r, _, _ := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{
			Backends: []config.Backend{{Name: "prod", URL: "https://am", PollInterval: 30 * time.Second}},
		}, nil
	})
	var spawned []time.Duration
	r.registry.setSpawn(func(c *config.Config) []*poll.Poller {
		spawned = append(spawned, pageInterval(c.Backends[0], c, resourceAlerts))
		return nil
	})

	require.IsType(t, app.ReloadedMsg{}, r.reload()())

	require.Equal(t, []time.Duration{30 * time.Second}, spawned)
}

// The mirror case: a reload that left every interval alone must not
// rebuild the pollers, because a rebuild drops the current fetch and
// restarts the cycle from zero.
func TestReload_LeavesThePollersAloneWhenNoIntervalMoved(t *testing.T) {
	t.Parallel()

	start := config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
	r, _, _ := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{
			Backends: []config.Backend{{Name: "prod", URL: "https://am"}},
			Theme:    config.Theme{Name: "nord"},
		}, nil
	})
	var spawned int
	r.registry.setSpawn(func(*config.Config) []*poll.Poller {
		spawned++
		return nil
	})

	require.IsType(t, app.ReloadedMsg{}, r.reload()())

	require.Zero(t, spawned)
}

// `:reload` runs on the bubbletea update goroutine, which is the one
// draining the message channel. A poller that is mid-send blocks
// there until the loop drains, so joining its goroutine from inside
// Update wedges the whole TUI: the send waits for the loop, the loop
// waits for the send, and Ctrl+C travels the same channel.
func TestPollerRegistry_RestartDoesNotJoinTheOutgoingPollers(t *testing.T) {
	t.Parallel()

	blocked := make(chan struct{})
	reg := &pollerRegistry{}
	stuck := poll.New(poll.Options{
		Tenant:   "prod",
		Resource: resourceAlerts,
		Interval: time.Hour,
		Fetch:    func(context.Context) (any, error) { return []backend.Alert{}, nil },
		Send:     func(tea.Msg) { <-blocked },
	})
	stuck.Start(t.Context())
	t.Cleanup(func() { close(blocked) })
	reg.setSpawn(func(*config.Config) []*poll.Poller { return nil })
	reg.pollers = []*poll.Poller{stuck}

	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.Restart(&config.Config{})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Restart joined a poller stuck in Send; in production that is the frozen TUI")
	}
}

// The alias half of the reload is covered end to end; this is the
// keys half. A key the user deleted from the file must stop firing,
// and the one they added must start.
func TestReload_ReAppliesTheKeyOverrides(t *testing.T) {
	t.Parallel()

	r, _, _ := reloadFixture(t, config.Config{}, func() (*config.Config, error) {
		return &config.Config{}, nil
	})
	var fired string
	r.dispatcher.SetAction(keys.LayerGlobal, "quit", "quit", "q", func() tea.Cmd {
		fired = "quit"
		return nil
	})
	r.dispatcher.SetAction(keys.LayerGlobal, "refresh", "refresh", "r", func() tea.Cmd {
		fired = "refresh"
		return nil
	})
	require.NoError(t, r.dispatcher.ApplyOverrides(config.KeyOverrides{"quit": {"Z"}}))
	r.deps.LoadKeys = func(string, string) (config.KeyOverrides, error) {
		return config.KeyOverrides{"refresh": {"Z"}}, nil
	}

	require.IsType(t, app.ReloadedMsg{}, r.reload()())

	consumed, _ := r.dispatcher.Dispatch("Z")
	require.True(t, consumed)
	require.Equal(t, "refresh", fired, "the reloaded file owns the key, not the one it replaced")
}

// An override the user deleted outright must stop firing, rather
// than linger until the next restart.
func TestReload_DropsAnOverrideDeletedFromTheFile(t *testing.T) {
	t.Parallel()

	r, _, _ := reloadFixture(t, config.Config{}, func() (*config.Config, error) {
		return &config.Config{}, nil
	})
	r.dispatcher.SetAction(keys.LayerGlobal, "quit", "quit", "q", func() tea.Cmd { return nil })
	require.NoError(t, r.dispatcher.ApplyOverrides(config.KeyOverrides{"quit": {"Z"}}))

	require.IsType(t, app.ReloadedMsg{}, r.reload()())

	consumed, _ := r.dispatcher.Dispatch("Z")
	require.False(t, consumed)
	consumed, _ = r.dispatcher.Dispatch("q")
	require.True(t, consumed, "the built-in key must survive the reload")
}

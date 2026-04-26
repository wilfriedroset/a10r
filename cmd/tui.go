// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/page/alerts"
	"github.com/wilfriedroset/a10r/internal/tui/page/silences"
	"github.com/wilfriedroset/a10r/internal/tui/page/status"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// runTUI assembles the bubbletea program and runs it. Called from
// the root command's RunE when no subcommand is supplied.
//
// v0.1 wiring is deliberately minimal: load config, build the
// first backend via the factory, push an alerts page as the home
// view, kick a poller for the alerts resource. Tenant fan-out,
// multi-backend pollers, and the silence-form / editor /
// receivers / groups / tenant pages are reachable via the `:`
// command bar — they just stay empty until the user navigates.
func runTUI(cmd *cobra.Command, flags *GlobalFlags) error {
	cfg, err := loadConfigForTUI(flags)
	if err != nil {
		return err
	}

	styles, err := loadStylesFor(cfg.Theme.Name)
	if err != nil {
		return err
	}

	registry := action.New()
	dispatcher := keys.New(nil)
	resolver := newResolver(*styles)

	// `gg` is a chord — the dispatcher buffers the first `g` and
	// fires the registered handler on the second within 500 ms.
	// Registering at LayerTable means every table-bodied page
	// (alerts, silences, receivers, groups, tenant) honours it
	// without per-page chord plumbing.
	dispatcher.Set(keys.LayerTable, "gg", func() tea.Cmd {
		return func() tea.Msg { return app.GoToFirstRowMsg{} }
	})

	a := app.NewApp(app.Options{
		Styles:     *styles,
		Registry:   registry,
		Dispatcher: dispatcher,
		CmdBar:     resolver,
	})

	prog := tea.NewProgram(a, tea.WithContext(cmd.Context()))

	// Spawn the poller for the first configured backend, if any.
	stopPoller := startBackendPoller(cmd.Context(), cfg, prog)
	defer stopPoller()

	// Push the alerts home page once the program is running. The
	// app.PushPage Cmd is invoked once to extract its message,
	// which Send routes through the Update loop on the next tick.
	go func() {
		homeFactory := func() app.Page { return alerts.New(*styles, time.Now) }
		prog.Send(app.PushPage(homeFactory)())
	}()

	_, err = prog.Run()
	return err
}

// loadConfigForTUI loads the user config; missing config returns
// a zero Config so the program still starts (the wizard wires
// from there in a future commit).
func loadConfigForTUI(flags *GlobalFlags) (*config.Config, error) {
	cfg, err := config.Load(loadOptsFromFlags(flags))
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			fmt.Fprintln(os.Stderr, "no config found — starting with empty backend list (run `a10r validate` after editing your config)")
			return &config.Config{}, nil
		}
		return nil, err
	}
	return cfg, nil
}

// loadOptsFromFlags translates persistent flags into config.LoadOpts.
// --config (a file path) splits into Dir + File so the loader reads
// the requested file directly; --config-dir falls back to the XDG
// resolution path with the canonical "a10r.yaml" basename.
func loadOptsFromFlags(flags *GlobalFlags) config.LoadOpts {
	if flags.ConfigPath != "" {
		return config.LoadOpts{
			Dir:  filepath.Dir(flags.ConfigPath),
			File: filepath.Base(flags.ConfigPath),
		}
	}
	return config.LoadOpts{Dir: flags.ConfigDir}
}

// loadStylesFor compiles the requested theme. Empty falls back to
// the default skin name.
func loadStylesFor(name string) (*theme.Styles, error) {
	if name == "" {
		name = theme.DefaultSkinName
	}
	return (&theme.Loader{}).Load(name)
}

// newResolver builds the cmdbar resolver with the v0.1 alias
// catalogue. Page factories close over the styles so each `:alerts`
// push lands a page wired to the same theme.
func newResolver(styles theme.Styles) *cmdbar.Resolver {
	r := cmdbar.New()
	r.Register("alerts", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return alerts.New(styles, time.Now) })
	})
	r.Register("silences", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return silences.New(styles, time.Now) })
	})
	r.Register("sil", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return silences.New(styles, time.Now) })
	})
	r.Register("status", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return status.New(styles, "") })
	})
	r.Register("q", func(_ []string) tea.Cmd { return tea.Quit })
	return r
}

// startBackendPoller spawns one Poller for the alerts resource of
// the first configured backend. Returns a stop func the caller
// defers to halt the goroutine on program exit. Returns a no-op
// stop when no backend is configured (first-run before the
// wizard) so the TUI launches with an empty list.
func startBackendPoller(ctx context.Context, cfg *config.Config, prog *tea.Program) func() {
	if len(cfg.Backends) == 0 {
		return func() {}
	}
	be := cfg.Backends[0]
	client, err := factory.Build(be)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backend %q: build failed: %v\n", be.Name, err)
		return func() {}
	}
	interval := be.PollInterval
	if interval == 0 {
		interval = cfg.Defaults.PollInterval
	}
	if interval == 0 {
		interval = time.Minute
	}
	p := poll.New(poll.Options{
		Tenant:   be.Name,
		Interval: interval,
		Fetch: func(ctx context.Context) (any, error) {
			return client.ListAlerts(ctx, backend.AlertFilter{})
		},
		Send: prog.Send,
	})
	p.Start(ctx)
	return p.Stop
}

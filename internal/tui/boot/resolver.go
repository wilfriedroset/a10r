// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
)

// newResolver builds the cmdbar resolver with the in-tree alias
// catalogue. Each `:command` handler hands an env-bound page factory
// to app.PushPage; pageEnv carries the shared deps so the resolver
// itself is just dispatch glue.
func newResolver(env *pageEnv) *cmdbar.Resolver {
	r := cmdbar.New()
	r.Register("alerts", func(args []string) tea.Cmd {
		ax, err := parseAlertsArgs(args)
		if err != nil {
			return flashWarnCmd(":alerts: " + err.Error())
		}
		return app.PushPage(func() app.Page { return newAlertsPage(env, ax.state, ax.filter) })
	})
	silencesFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return newSilencesPage(env) })
	}
	r.RegisterGroup([]string{"silences", "sil"}, silencesFactory)
	r.Register("status", func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return newStatusPage(env) })
	})
	receiversFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return newReceiversPage(env) })
	}
	r.RegisterGroup([]string{"receivers", "rec"}, receiversFactory)
	groupsFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return newGroupsPage(env) })
	}
	r.RegisterGroup([]string{"groups", "gr"}, groupsFactory)
	drill := func(name string) (app.Page, error) { return newTenantConfigPage(env, name) }
	tenantFactory := func(_ []string) tea.Cmd {
		return app.PushPage(func() app.Page { return newTenantPage(env, drill) })
	}
	r.RegisterGroup([]string{"tenant", "tenants"}, tenantFactory)
	// `:q` mirrors the `q` / Ctrl+C bindings — emits the quit-
	// precursor so the App can Close() every page on the stack
	// (cancelling in-flight bulk fanouts, silence-form writes,
	// editor updates, status fetches) before bubbletea stops.
	r.Register("q", func(_ []string) tea.Cmd {
		return func() tea.Msg { return app.QuitRequestedMsg{} }
	})
	return r
}

// applyUserKeyOverrides reads the user keys file via the Deps-
// configured loader and applies every user-extra key to its
// matching action's existing (layer, handler) pair on the
// dispatcher. Missing file is not an error per LoadKeys's
// contract — operators who don't curate keys see no mention of
// the feature and pay nothing for it.
func applyUserKeyOverrides(d *keys.Dispatcher, configDir string, load func(string, string) (config.KeyOverrides, error)) error {
	overrides, err := load(configDir, config.DefaultKeysProfile)
	if err != nil {
		return err
	}
	if len(overrides) == 0 {
		return nil
	}
	return d.ApplyOverrides(overrides) //nolint:wrapcheck // ApplyOverrides errors are already self-describing ("unknown action ...")
}

// registerUserAliases reads aliases.yaml via the Deps-configured
// loader, validates the entries against the resolver's built-in
// alias set, and registers every user alias on the resolver.
// Returns the count of registered aliases so callers can surface
// "n user aliases loaded" as a startup signal.
//
// Missing file is not an error per the loader contract — operators
// who don't curate aliases see no mention of the feature and pay
// nothing for it.
func registerUserAliases(r *cmdbar.Resolver, configDir string, load func(string) (config.AliasMap, error)) (int, error) {
	user, err := load(configDir)
	if err != nil {
		return 0, err
	}
	for short, expanded := range user {
		if err := r.RegisterUser(short, expanded); err != nil {
			return 0, fmt.Errorf("register %q: %w", short, err)
		}
	}
	return len(user), nil
}

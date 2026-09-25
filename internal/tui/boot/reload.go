// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"fmt"
	"reflect"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/cmdbar"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
)

// frozenConfigChanged reports whether the new config differs in any
// field the running session was built from and cannot rebuild:
// the backend list and everything the clients were constructed with,
// plus the log sink the audit trail writes to.
//
// `:reload` refuses rather than applies when this is true. Swapping
// a backend URL under a live client set would leave the pollers, the
// tenant scope and the page stack talking to one Alertmanager while
// the config page describes another, and re-opening the log sink
// mid-session loses the ordering guarantee the audit trail relies on.
func frozenConfigChanged(old, next *config.Config) bool {
	if old.Log != next.Log {
		return true
	}
	// Frozen for the same reason as Log, despite living on Defaults:
	// initLogger builds the handler's encoder from it, and the sink
	// keeps that encoder for the session.
	if old.Defaults.LogFormat != next.Defaults.LogFormat {
		return true
	}
	if len(old.Backends) != len(next.Backends) {
		return true
	}
	for i := range old.Backends {
		if !reflect.DeepEqual(frozenBackend(old.Backends[i]), frozenBackend(next.Backends[i])) {
			return true
		}
	}
	return false
}

// frozenBackend clears the fields a live session can swap, leaving
// the part a reload must refuse to change. Clearing rather than
// listing puts a field added later on the frozen side by default,
// which costs a restart instead of a session that disagrees with its
// own configuration.
func frozenBackend(be config.Backend) config.Backend {
	be.ReadOnly = false
	be.PollInterval = 0
	return be
}

// reloader re-reads the config file and applies the part a live
// session can change. It is the wiring half of `:reload`: the App
// owns the refusal behind an open form, and everything that needs a
// loader, the flags or the poller set lives here.
type reloader struct {
	deps       Deps
	flags      *config.CLIFlags
	env        *pageEnv
	registry   *pollerRegistry
	resolver   *cmdbar.Resolver
	dispatcher *keys.Dispatcher
	configDir  string
}

// reload re-reads the config and either refuses the whole reload or
// applies it whole. There is no partial apply: a half-applied config
// is a session no one can reason about.
//
// reload does its file reads synchronously rather than inside the
// returned Cmd on purpose: it mutates the resolver, the dispatcher
// and the shared pageEnv, none of which are safe off the update
// goroutine. Only the resulting message is deferred.
func (r *reloader) reload() tea.Cmd {
	cfg, err := r.deps.LoadConfig(LoadOptsFromFlags(r.flags))
	if err != nil {
		return flashWarnCmd(fmt.Sprintf("reload: %v", err))
	}
	effective, err := resolveEffectiveConfig(r.flags, cfg)
	if err != nil {
		return flashWarnCmd(fmt.Sprintf("reload: %v", err))
	}
	effCfg := effective.Config
	live := r.env.Session.Config()
	if frozenConfigChanged(live, &effCfg) {
		return flashWarnCmd("reload: backends or log changed, restart a10r")
	}
	aliases, err := r.deps.LoadAliases(r.configDir)
	if err != nil {
		return flashWarnCmd(fmt.Sprintf("reload: %v", err))
	}
	overrides, err := r.deps.LoadKeys(r.configDir, config.DefaultKeysProfile)
	if err != nil {
		return flashWarnCmd(fmt.Sprintf("reload: %v", err))
	}
	if name := r.unknownAction(overrides); name != "" {
		return flashWarnCmd(fmt.Sprintf("reload: unknown action %q in the keys file", name))
	}
	if err := r.resolver.ReplaceUser(aliases); err != nil {
		return flashWarnCmd(fmt.Sprintf("reload: %v", err))
	}
	r.dispatcher.ClearOverrides()
	// Cannot fail past unknownAction, which is what keeps the alias
	// swap above safe: an error here would leave the aliases applied
	// and the keys cleared. A new error condition in ApplyOverrides
	// has to grow a matching pre-check, not just this branch.
	if err := r.dispatcher.ApplyOverrides(overrides); err != nil {
		return flashWarnCmd(fmt.Sprintf("reload: %v", err))
	}

	restart := restartFields(live, &effCfg)
	// The session keeps what the run is doing, not what the file asks
	// for, so a second reload still names it and `:config` does not
	// claim a persistence this run never opened.
	effCfg.TUI.Remember = live.TUI.Remember
	r.env.Session.Apply(effCfg)
	r.registry.Sync(live)
	return func() tea.Msg { return app.ReloadedMsg{Restart: restart} }
}

// restartFields names the settings the new file changed that the
// running session applies only at startup. tui.remember is one because
// the state store is opened once: turning persistence on mid-session
// would write a file the run never read.
func restartFields(old, next *config.Config) []string {
	var out []string
	if old.TUI.Remember != next.TUI.Remember {
		out = append(out, "tui.remember")
	}
	return out
}

// unknownAction names the first action in the keys file the
// dispatcher does not know, or empty when every name resolves.
// Called before anything is applied, because ApplyOverrides rejects
// an unknown name halfway through an otherwise good file and a
// reload must not land in halves. Sorted, so a file with two bad
// names flashes the same one every time rather than alternating.
func (r *reloader) unknownAction(overrides config.KeyOverrides) string {
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !r.dispatcher.HasAction(name) {
			return name
		}
	}
	return ""
}

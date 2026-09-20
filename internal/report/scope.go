// SPDX-License-Identifier: Apache-2.0

package report

import (
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/uistate"
)

// RememberedScope reports the tenant scope the next start would
// restore, pruned against the backends the config still declares.
// `a10r info` and the TUI's `:info` page both call it so neither can
// name a tenant the other would silently drop.
//
// Empty covers three cases the report all prints the same way: no
// config, tui.remember off, and nothing usable stored.
func RememberedScope(cfg *config.Config, store *uistate.Store) string {
	if cfg == nil || !cfg.TUI.Remember {
		return ""
	}
	names := make([]string, len(cfg.Backends))
	for i, b := range cfg.Backends {
		names[i] = b.Name
	}
	pruned, _ := uistate.PruneScope(store.Scope(), names)
	if pruned == config.ScopeAll {
		return ""
	}
	return pruned
}

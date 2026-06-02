// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenant"
)

// scopeAll is the k9s-convention multi-tenant scope label. Used
// by every list page's title and the poller's refresh router so
// "every backend" reads the same token everywhere.
const scopeAll = "all"

// backendNames returns the configured tenant names in
// configuration order. Used to populate the panel's tenant-
// shortcut column.
func backendNames(cfg *config.Config) []string {
	out := make([]string, len(cfg.Backends))
	for i, b := range cfg.Backends {
		out[i] = b.Name
	}
	return out
}

// buildTenantRows assembles the tenant page's row list from
// configured backends + the startup-fetched version map.
// Backends whose factory build failed are still surfaced (the
// user wants to see the misconfigured entry in the tenant table)
// but with an empty version that renders as "—".
func buildTenantRows(cfg *config.Config, versions map[string]string) []tenant.Row {
	rows := make([]tenant.Row, 0, len(cfg.Backends))
	for _, be := range cfg.Backends {
		rows = append(rows, tenant.Row{
			Name:    be.Name,
			URL:     be.URL,
			Version: versions[be.Name],
		})
	}
	return rows
}

// tenantConfigIndex returns a map from backend name to its
// resolved config.Backend struct so the tenant-config drill
// factory can hand the right entry to the inspector page.
func tenantConfigIndex(cfg *config.Config) map[string]config.Backend {
	out := make(map[string]config.Backend, len(cfg.Backends))
	for _, be := range cfg.Backends {
		out[be.Name] = be
	}
	return out
}

// scopeFor returns the tenant label rendered in the alerts page
// title. Single backend → its name; two or more → "all" (the
// k9s convention for the multi-namespace case). Empty config →
// "all" so the title still reads cleanly even pre-wizard.
func scopeFor(cfg *config.Config) string {
	switch len(cfg.Backends) {
	case 0:
		return scopeAll
	case 1:
		return cfg.Backends[0].Name
	default:
		return scopeAll
	}
}

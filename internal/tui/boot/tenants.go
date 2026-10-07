// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"log/slog"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/page/tenant"
)

// scopeAll is the k9s-convention multi-tenant scope label. Used
// by every list page's title and the poller's refresh router so
// "every backend" reads the same token everywhere.
const scopeAll = "all"

// backendNames returns the configured tenant names in
// configuration order.
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
// but with an empty version that renders as "—". The URL is
// redacted because the column stays on screen for the whole
// session.
func buildTenantRows(cfg *config.Config, versions map[string]string) []tenant.Row {
	rows := make([]tenant.Row, 0, len(cfg.Backends))
	for _, be := range cfg.Backends {
		rows = append(rows, tenant.Row{
			Name:    be.Name,
			URL:     config.RedactURL(be.URL),
			Version: versions[be.Name],
		})
	}
	return rows
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

// logUnmatchedTenants warns once per `guardrails:` tenant glob that
// names no configured backend. A warning rather than a startup
// error per ADR 0049: one config.d fragment is shared across
// machines that do not all have every tenant. The capture the
// `:config` page reads is open around this call, so the warning
// lands there as well as in the log file.
func logUnmatchedTenants(logger *slog.Logger, cfg *config.Config) {
	for _, glob := range cfg.Guardrails.UnmatchedTenants(backendNames(cfg)) {
		logger.Warn("guardrail tenant glob matches no configured backend",
			slog.String("glob", glob))
	}
}

// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"log/slog"
	"strings"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
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

// writePolicy is the rule set the TUI enforces: one deny per backend
// carrying `read_only: true`, ahead of the configured `guardrails:`.
// Routing the per-backend flag through the same evaluator is what
// makes configuration.md's precedence true inside the TUI, where a
// list page unions rows from several tenants and a page-wide switch
// cannot say "prod is frozen, staging is not".
//
// Prepended so a tenant denied by both the flag and a rule quotes the
// flag's reason: a rule cannot be edited around a frozen backend.
func writePolicy(cfg *config.Config) guardrail.Set {
	out := make(guardrail.Set, 0, len(cfg.Backends)+len(cfg.Guardrails))
	for _, be := range cfg.Backends {
		if be.ReadOnly {
			out = append(out, guardrail.Rule{
				Tenants: []string{globQuote(be.Name)},
				Deny:    true,
				Reason:  "backend is read_only",
			})
		}
	}
	return append(out, cfg.Guardrails...)
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

// globQuote escapes the pattern syntax path.Match reads. A rule's
// tenant list is a glob while a backend name is free-form, so an
// unquoted `prod[1]` would compile to a pattern matching anything but
// its own backend and the deny would fail open on the very backend
// the user froze.
func globQuote(name string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(name)
}

// sessionReadOnly reports whether the session has nothing writable at
// all, which is the one case the page-wide switch still describes
// honestly: hiding the Dangerous bindings beats advertising a key
// that can only ever refuse. Short of that the bindings stay up and
// writePolicy refuses per row.
//
// Deliberately blind to the tenant scope: the user re-scopes at
// runtime, and chips that appear and vanish as they walk the scope
// picker read as a bug rather than a policy.
func sessionReadOnly(cfg *config.Config) bool {
	if cfg.Defaults.ReadOnly {
		return true
	}
	for _, be := range cfg.Backends {
		if !be.ReadOnly {
			return false
		}
	}
	return len(cfg.Backends) > 0
}

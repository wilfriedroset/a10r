// SPDX-License-Identifier: Apache-2.0

// Package session owns the effective configuration of a running TUI
// and the values derived from it. A page holds the *Session and asks
// at the point of use, so a reload that calls Apply reaches every
// page already on the stack without a message per changed field.
//
// A Session belongs to the Update goroutine: Apply and every read run
// there, which is why it carries no lock.
package session

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
)

// Session is the live configuration plus its derivations.
type Session struct {
	cfg        *config.Config
	readOnly   bool
	guardrails guardrail.Set
	tenants    map[string]config.Backend
}

// New copies cfg into a Session whose Config pointer stays the same
// across every later Apply.
func New(cfg config.Config) *Session {
	s := &Session{cfg: &config.Config{}}
	s.Apply(cfg)
	return s
}

// OrEmpty returns s, or a Session over the zero config when s is nil,
// so a page built without one reads as writable with no guardrails.
func OrEmpty(s *Session) *Session {
	if s == nil {
		return New(config.Config{})
	}
	return s
}

// Apply swaps in cfg and redoes every derivation, so a report closure
// that captured Config reads the new file.
func (s *Session) Apply(cfg config.Config) {
	*s.cfg = cfg
	s.readOnly = sessionReadOnly(s.cfg)
	s.guardrails = writePolicy(s.cfg)
	s.tenants = tenantIndex(s.cfg)
}

// Config returns the live configuration. Callers must not mutate it.
func (s *Session) Config() *config.Config { return s.cfg }

// ReadOnly reports whether the session has nothing writable at all.
func (s *Session) ReadOnly() bool { return s.readOnly }

// Guardrails returns the per-tenant write policy every write surface
// asks through Decide.
func (s *Session) Guardrails() guardrail.Set { return s.guardrails }

// BulkConcurrency returns the per-tenant worker-pool size of a bulk
// fan-out.
func (s *Session) BulkConcurrency() int { return s.cfg.Defaults.BulkConcurrencyOrDefault() }

// AlertColumns are the label columns of the alerts list.
func (s *Session) AlertColumns() []config.Column { return s.cfg.Pages.Alerts.Columns }

// GroupDetailColumns are the label columns of the group drill-down,
// which the alerts list constructs and boot never sees.
func (s *Session) GroupDetailColumns() []config.Column { return s.cfg.Pages.GroupDetail.Columns }

// TenantConfig backs the tenant-config drill, which is keyed by name.
func (s *Session) TenantConfig(name string) (config.Backend, bool) {
	be, ok := s.tenants[name]
	return be, ok
}

func tenantIndex(cfg *config.Config) map[string]config.Backend {
	out := make(map[string]config.Backend, len(cfg.Backends))
	for _, be := range cfg.Backends {
		out[be.Name] = be
	}
	return out
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

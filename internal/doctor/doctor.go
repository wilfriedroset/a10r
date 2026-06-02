// SPDX-License-Identifier: Apache-2.0

// Package doctor runs preflight health checks against every
// configured backend, reporting per-check severity (ok / warning /
// error) so an operator can confirm a fresh a10r config before the
// TUI launches. The Checker interface is the extension seam: each
// bundled check (reachability, auth, version floor, …) lives in
// checks.go as a small struct implementing the same contract.
//
// Doctor is invoked from `a10r doctor`; see cmd/doctor.go for the
// cobra wiring. Output goes through internal/output's Table
// (default), JSON, or YAML so CI/CD wrappers can consume the
// result structurally.
package doctor

import (
	"context"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// Severity classifies a Result as informational, a warning the
// operator should investigate but is not currently blocking, or
// an error that prevents normal operation. The underlying type is
// string so JSON/YAML output emits the lowercase name directly
// (no MarshalJSON needed) and a zero value is the empty string —
// loud, not the silent "ok" an iota-typed enum would mask.
type Severity string

const (
	// SeverityOK means the check ran successfully and the backend
	// passes. Doctor still reports the line so the operator sees
	// every probe that ran (clear list).
	SeverityOK Severity = "ok"

	// SeverityWarning means the backend is operational but
	// something is off (e.g. server clock skewed, TLS cert
	// expiring soon). Doctor proceeds without aborting.
	SeverityWarning Severity = "warning"

	// SeverityError means the check failed in a way that prevents
	// a10r from working correctly (unreachable, version floor
	// violation). The doctor command exits non-zero when a write-
	// side health check lands — see ADR 0009 for the exit-code table.
	SeverityError Severity = "error"
)

// String renders s for the table column. The empty zero value
// renders as "unknown" so a Result that forgets to set Severity
// still produces something visible rather than a blank cell.
func (s Severity) String() string {
	if s == "" {
		return "unknown"
	}
	return string(s)
}

// Result is what a Checker emits for one (backend, check) pair.
// Backend is the configured name; Check is the registered name of
// the Checker; Severity classifies the outcome; Message is the
// one-line operator-facing detail. Empty Message is allowed for
// SeverityOK rows where "passed" is the whole story.
type Result struct {
	Backend  string   `json:"backend" yaml:"backend"`
	Check    string   `json:"check" yaml:"check"`
	Severity Severity `json:"severity" yaml:"severity"`
	Message  string   `json:"message,omitempty" yaml:"message,omitempty"`
}

// Checker is the per-check contract. Name returns the registered
// short identifier (used in the table column and `--only` flag);
// Run performs the check against one backend and returns one
// Result. Implementations must not panic on any backend state and
// must respect ctx cancellation.
type Checker interface {
	Name() string
	Run(ctx context.Context, b config.Backend, c backend.Client) Result
}

// Run executes every checker against every backend and returns the
// flat result list in (backend, checker) registration order.
// Cancellation aborts mid-run; partial results are returned.
//
// The function deliberately accepts the slice of Checker rather
// than discovering a global registry: tests construct narrow
// suites (one checker, one backend) and the cmd-layer adds the
// `--only` filter at registration time.
func Run(ctx context.Context, backends []config.Backend, clients map[string]backend.Client, checkers []Checker) []Result {
	results := make([]Result, 0, len(backends)*len(checkers))
	for _, b := range backends {
		client := clients[b.Name]
		for _, ch := range checkers {
			if ctx.Err() != nil {
				return results
			}
			results = append(results, ch.Run(ctx, b, client))
		}
	}
	return results
}

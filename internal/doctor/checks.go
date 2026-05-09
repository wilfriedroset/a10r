// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// DefaultCheckers returns the bundled checker suite in the order
// doctor runs them. Reachability first because every other check
// needs the backend to respond at all; auth next so credential
// failures surface before semantic checks; version-floor last
// because it depends on a successful Status() call which the
// previous checks already exercise.
func DefaultCheckers() []Checker {
	return []Checker{
		ReachabilityChecker{},
		AuthChecker{},
		VersionFloorChecker{},
	}
}

// ReachabilityChecker probes backend liveness via /-/ready. The
// backend.Client interface does not expose ProbeReady — the type
// assertion to backend.Prober gates the check so a future test
// stub can opt out by not implementing the smaller interface.
type ReachabilityChecker struct{}

// Name implements Checker.
func (ReachabilityChecker) Name() string { return "reachability" }

// Run implements Checker. nil client (factory.Build failed at
// startup) reports as a single Error so the operator sees the
// configured backend even when its client could not be
// constructed.
func (ReachabilityChecker) Run(ctx context.Context, b config.Backend, c backend.Client) Result {
	if c == nil {
		return Result{
			Backend:  b.Name,
			Check:    "reachability",
			Severity: SeverityError,
			Message:  "client construction failed at startup; check `a10r validate`",
		}
	}
	prober, ok := c.(backend.Prober)
	if !ok {
		return Result{
			Backend:  b.Name,
			Check:    "reachability",
			Severity: SeverityWarning,
			Message:  "client does not implement Prober — skipping",
		}
	}
	if err := prober.ProbeReady(ctx); err != nil {
		return Result{
			Backend:  b.Name,
			Check:    "reachability",
			Severity: SeverityError,
			Message:  err.Error(),
		}
	}
	return Result{Backend: b.Name, Check: "reachability", Severity: SeverityOK}
}

// AuthChecker calls Status() and classifies the response. 401/403
// surfaces as Error; transport-layer failures (already covered by
// ReachabilityChecker) surface as Warning to avoid double-reporting
// the same root cause.
type AuthChecker struct{}

// Name implements Checker.
func (AuthChecker) Name() string { return "auth" }

// Run implements Checker.
func (AuthChecker) Run(ctx context.Context, b config.Backend, c backend.Client) Result {
	if c == nil {
		return Result{
			Backend:  b.Name,
			Check:    "auth",
			Severity: SeverityError,
			Message:  "client construction failed at startup",
		}
	}
	if _, err := c.Status(ctx); err != nil {
		switch {
		case errors.Is(err, backend.ErrUnauthorized):
			return Result{
				Backend:  b.Name,
				Check:    "auth",
				Severity: SeverityError,
				Message:  fmt.Sprintf("authentication rejected: %s", err),
			}
		case errors.Is(err, backend.ErrUnreachable):
			return Result{
				Backend:  b.Name,
				Check:    "auth",
				Severity: SeverityWarning,
				Message:  "backend unreachable; auth not exercised",
			}
		default:
			return Result{
				Backend:  b.Name,
				Check:    "auth",
				Severity: SeverityError,
				Message:  err.Error(),
			}
		}
	}
	return Result{Backend: b.Name, Check: "auth", Severity: SeverityOK}
}

// VersionFloorChecker parses Status().VersionInfo.Version and
// compares against backend.MinAlertmanagerVersion. Below floor →
// Error; unparseable version → Warning (the operator's AM is
// reporting an unrecognised string but isn't necessarily broken);
// above-or-equal → OK with the version reported in the message.
type VersionFloorChecker struct{}

// Name implements Checker.
func (VersionFloorChecker) Name() string { return "version-floor" }

// Run implements Checker.
func (VersionFloorChecker) Run(ctx context.Context, b config.Backend, c backend.Client) Result {
	if c == nil {
		return Result{
			Backend:  b.Name,
			Check:    "version-floor",
			Severity: SeverityError,
			Message:  "client construction failed at startup",
		}
	}
	st, err := c.Status(ctx)
	if err != nil {
		return Result{
			Backend:  b.Name,
			Check:    "version-floor",
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("status unavailable: %s", err),
		}
	}
	got, err := backend.ParseVersion(st.Version.Version)
	if err != nil {
		return Result{
			Backend:  b.Name,
			Check:    "version-floor",
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("backend reported unrecognised version %q: %s", st.Version.Version, err),
		}
	}
	floor, err := backend.ParseVersion(backend.MinAlertmanagerVersion)
	if err != nil {
		// Unreachable in practice — the const is regression-tested
		// to parse — but if a typo ever lands the error surfaces
		// here rather than panicking.
		return Result{
			Backend:  b.Name,
			Check:    "version-floor",
			Severity: SeverityError,
			Message:  fmt.Sprintf("internal: floor constant unparseable: %s", err),
		}
	}
	if got.Compare(floor) < 0 {
		return Result{
			Backend:  b.Name,
			Check:    "version-floor",
			Severity: SeverityError,
			Message:  fmt.Sprintf("backend reports %s; a10r requires >= %s", got, floor),
		}
	}
	return Result{
		Backend:  b.Name,
		Check:    "version-floor",
		Severity: SeverityOK,
		Message:  fmt.Sprintf("backend %s >= floor %s", got, floor),
	}
}

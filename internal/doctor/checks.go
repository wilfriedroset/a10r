// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	a10rtls "github.com/wilfriedroset/a10r/internal/backend/tls"
	"github.com/wilfriedroset/a10r/internal/config"
)

// DefaultCheckers returns the bundled checker suite in the order
// doctor runs them. Reachability first because every other check
// needs the backend to respond at all; auth next so credential
// failures surface before semantic checks; version-floor next
// because it depends on a successful Status() call which the
// previous checks already exercise. The remaining three —
// tls-expiry, capabilities, clock-skew — are independent of each
// other and run in declaration order; their position after
// version-floor keeps the table reading top-to-bottom from
// "can a10r talk to the backend?" through "is its certificate
// healthy and its clock sane?".
func DefaultCheckers() []Checker {
	return []Checker{
		ReachabilityChecker{},
		AuthChecker{},
		VersionFloorChecker{},
		TLSExpiryChecker{},
		CapabilitiesChecker{},
		ClockSkewChecker{},
	}
}

// ReachabilityChecker probes backend liveness via /-/ready. The
// backend.Client interface does not expose ProbeReady — the type
// assertion to backend.Prober gates the check so a future test
// stub can opt out by not implementing the smaller interface.
type ReachabilityChecker struct{}

func (ReachabilityChecker) Name() string { return "reachability" }

// Run reports a single Error when the client is nil — factory.Build
// failed at startup, but the operator must still see the configured
// backend in the result table.
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
//
// Per ADR 0039 the 401/403 message picks up a tenant-hint when
// the operator has no `tenant_header:` configured (most common
// remediation; surfacing it on the doctor table itself saves a
// round-trip to docs), and a 404 on Status() triggers one
// alertmanager-mount probe — only a 2xx retry downgrades to
// Warning so doctor never claims a fix it cannot prove
// end-to-end.
type AuthChecker struct{}

func (AuthChecker) Name() string { return "auth" }

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
			msg := fmt.Sprintf("authentication rejected: %s", err)
			if b.TenantHeader == "" {
				msg += " — if backend is multi-tenant Mimir, set tenant_header: X-Scope-OrgID and tenant: <org> in a10r.yaml"
			}
			return Result{
				Backend:  b.Name,
				Check:    "auth",
				Severity: SeverityError,
				Message:  msg,
			}
		case errors.Is(err, backend.ErrUnreachable):
			return Result{
				Backend:  b.Name,
				Check:    "auth",
				Severity: SeverityWarning,
				Message:  "backend unreachable; auth not exercised",
			}
		default:
			// Adding `prefix: /alertmanager` cannot fix a 422 / 400 /
			// decode failure, so a coincidentally-working mount probe
			// must not downgrade non-404 errors (ADR 0039 honesty bar).
			if errors.Is(err, backend.ErrNotFound) {
				if prober, ok := c.(backend.Prober); ok && prober.ProbeAlertmanagerMount(ctx) == nil {
					return Result{
						Backend:  b.Name,
						Check:    "auth",
						Severity: SeverityWarning,
						Message: "Status() failed but /alertmanager/api/v2/status returned 200 — " +
							"set prefix: /alertmanager in a10r.yaml",
					}
				}
			}
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

func (VersionFloorChecker) Name() string { return "version-floor" }

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

// tlsExpiryWarnThreshold is the rolling window before NotAfter at
// which TLSExpiryChecker downgrades from OK to Warning. 30 days is
// the de-facto industry default (matches Let's Encrypt's renewal
// window and Prometheus's bundled certmanager template) and gives
// the operator a calendar's-worth of slack between "first warning"
// and "expired and broken".
const tlsExpiryWarnThreshold = 30 * 24 * time.Hour

// tlsCertProbe is the test seam for TLSExpiryChecker. Production
// uses a10rtls.ProbeCert directly; tests inject a stub that returns
// a pinned *x509.Certificate so the checker can be exercised with
// arbitrary NotAfter values without spinning up a TLS server with
// a custom signing authority.
type tlsCertProbe func(ctx context.Context, url string) (*x509.Certificate, error)

// TLSExpiryChecker probes the backend URL's TLS certificate and
// classifies its remaining validity. Behaviour:
//
//   - non-https URL → SeverityOK with a "n/a" message (TLS is not
//     configured; nothing to check). Mirrors `kubectl --insecure`'s
//     "this is fine, just say so" stance.
//   - dial / handshake failure → SeverityError. The reachability
//     and auth checkers already cover plain "can't reach the
//     backend"; a TLS-handshake failure here is a TLS-specific
//     diagnostic (expired CA, unknown signer) the operator
//     should investigate distinct from "is it up at all".
//   - cert.NotAfter already in the past → SeverityError.
//   - cert.NotAfter within 30 days → SeverityWarning.
//   - cert.NotAfter beyond 30 days → SeverityOK with the expiry
//     date in the message.
//
// The probe is a fresh TCP+TLS dial via internal/backend/tls; it
// deliberately does NOT reach into the constructed http.Client's
// transport (the auth and host-pinning chain is opaque from the
// outside, and a transport-internal probe would re-dial through
// the operator's proxy / TLS-pinning configuration which is the
// wrong question for "does the cert expire soon").
type TLSExpiryChecker struct {
	// probe is the test seam. Nil delegates to the production
	// a10rtls.ProbeCert with a default dialer.
	probe tlsCertProbe

	// now is the test seam for the current time. Nil delegates to
	// time.Now. The 30-day threshold is computed against this so
	// tests can pin the boundary case deterministically.
	now func() time.Time
}

func (TLSExpiryChecker) Name() string { return "tls-expiry" }

func (t TLSExpiryChecker) Run(ctx context.Context, b config.Backend, _ backend.Client) Result {
	probe := t.probe
	if probe == nil {
		probe = func(ctx context.Context, url string) (*x509.Certificate, error) {
			return a10rtls.ProbeCert(ctx, url, nil)
		}
	}
	now := t.now
	if now == nil {
		now = time.Now
	}

	cert, err := probe(ctx, b.URL)
	if errors.Is(err, a10rtls.ErrNotHTTPS) {
		return Result{
			Backend:  b.Name,
			Check:    "tls-expiry",
			Severity: SeverityOK,
			Message:  "n/a (backend URL is not https)",
		}
	}
	if err != nil {
		return Result{
			Backend:  b.Name,
			Check:    "tls-expiry",
			Severity: SeverityError,
			Message:  fmt.Sprintf("tls probe failed: %s", err),
		}
	}

	remaining := cert.NotAfter.Sub(now())
	switch {
	case remaining <= 0:
		return Result{
			Backend:  b.Name,
			Check:    "tls-expiry",
			Severity: SeverityError,
			Message:  fmt.Sprintf("certificate expired at %s", cert.NotAfter.Format(time.RFC3339)),
		}
	case remaining < tlsExpiryWarnThreshold:
		return Result{
			Backend:  b.Name,
			Check:    "tls-expiry",
			Severity: SeverityWarning,
			Message: fmt.Sprintf("certificate expires in %s (NotAfter %s)",
				remaining.Round(time.Hour), cert.NotAfter.Format(time.RFC3339)),
		}
	default:
		return Result{
			Backend:  b.Name,
			Check:    "tls-expiry",
			Severity: SeverityOK,
			Message:  fmt.Sprintf("certificate valid until %s", cert.NotAfter.Format(time.RFC3339)),
		}
	}
}

// capabilityProbe runs one capability's smoke call against the
// constructed client. Returning nil means the backend honours the
// capability; returning a non-nil error means the cap is enabled
// in config but the backend rejects it (e.g. ConfigAPI on vanilla
// AM 404s). Errors that wrap backend.ErrUnreachable are treated by
// CapabilitiesChecker as transport failures (the reachability
// checker already covered the same root cause) and surface as
// SeverityWarning instead of Error.
type capabilityProbe func(ctx context.Context, c backend.Client) error

// defaultProbes returns the smoke-call map CapabilitiesChecker uses
// when CapabilitiesChecker.probes is nil. Built lazily so the registry
// is not shared mutable state; tests override individual entries via
// the struct field rather than reaching for a package-level handle.
// Probes treat backend.ErrUnsupported as a hard mismatch ("config says
// yes, backend says no") regardless of the underlying HTTP status;
// other errors propagate verbatim so the message tells the operator
// what failed.
func defaultProbes() map[string]capabilityProbe {
	return map[string]capabilityProbe{
		"config_api": func(ctx context.Context, c backend.Client) error {
			_, err := c.GetConfig(ctx)
			if err != nil {
				return fmt.Errorf("get config: %w", err)
			}
			return nil
		},
		"tenant_admin": func(ctx context.Context, c backend.Client) error {
			_, err := c.ListTenantConfigs(ctx)
			if err != nil {
				return fmt.Errorf("list tenant configs: %w", err)
			}
			return nil
		},
		"ring": func(ctx context.Context, c backend.Client) error {
			_, err := c.RingStatus(ctx)
			if err != nil {
				return fmt.Errorf("get ring status: %w", err)
			}
			return nil
		},
	}
}

// enabledCapabilities returns the capability names whose flag is
// set in caps. Returned in a fixed order (config_api, tenant_admin,
// ring) so result messages are deterministic regardless of the
// caller's iteration order.
func enabledCapabilities(caps config.Capabilities) []string {
	var out []string
	if caps.ConfigAPI {
		out = append(out, "config_api")
	}
	if caps.TenantAdmin {
		out = append(out, "tenant_admin")
	}
	if caps.Ring {
		out = append(out, "ring")
	}
	return out
}

// CapabilitiesChecker exercises every Capabilities flag set in the
// backend's config by calling the corresponding API once and
// classifying the response. Behaviour:
//
//   - no caps enabled → SeverityOK with a "no capabilities
//     configured" message (clear that the check ran).
//   - all enabled caps respond → SeverityOK with the list of
//     verified caps in the message.
//   - one or more caps return ErrUnsupported / 404 →
//     SeverityError, message names the failing caps.
//   - one or more caps fail with a transport error → SeverityWarning
//     (reachability already reported the underlying failure;
//     downgrading here avoids double-counting it as a hard
//     capability mismatch).
type CapabilitiesChecker struct {
	// probes is the test seam. Nil falls back to defaultProbes();
	// fixtures swap individual probes to pin per-cap responses without
	// spinning up an HTTP server.
	probes map[string]capabilityProbe
}

func (CapabilitiesChecker) Name() string { return "capabilities" }

// result stamps a Result with the backend name and this checker's
// Name(), leaving Run to decide only severity and message.
func (cc CapabilitiesChecker) result(b config.Backend, sev Severity, msg string) Result {
	return Result{Backend: b.Name, Check: cc.Name(), Severity: sev, Message: msg}
}

func (cc CapabilitiesChecker) Run(ctx context.Context, b config.Backend, c backend.Client) Result {
	if c == nil {
		return cc.result(b, SeverityError, "client construction failed at startup")
	}
	enabled := enabledCapabilities(b.Capabilities)
	if len(enabled) == 0 {
		return cc.result(b, SeverityOK, "no capabilities configured")
	}

	probes := cc.probes
	if probes == nil {
		probes = defaultProbes()
	}

	var (
		mismatched []string
		transport  []string
		verified   []string
	)
	for _, name := range enabled {
		probe, ok := probes[name]
		if !ok {
			// A flag exists in config but no probe is registered:
			// treat as a mismatch so a future capability addition
			// without a corresponding probe does not silently pass.
			mismatched = append(mismatched, name+" (no probe registered)")
			continue
		}
		switch err := probe(ctx, c); {
		case err == nil:
			verified = append(verified, name)
		case errors.Is(err, backend.ErrUnreachable), backend.Retryable(err):
			// Transport-class failure: ErrUnreachable (DNS / refused
			// / timeout) AND 5xx / 429 (Retryable() == true). Both
			// indicate the backend is presently sick rather than
			// "doesn't speak this capability"; the operator should
			// see one warning here, not a confusing "mismatch" they
			// will later trace back to a 503.
			transport = append(transport, name)
		default:
			mismatched = append(mismatched, fmt.Sprintf("%s (%s)", name, err))
		}
	}

	switch {
	case len(mismatched) > 0:
		return cc.result(b, SeverityError, fmt.Sprintf("capability mismatch: %v", mismatched))
	case len(transport) > 0:
		return cc.result(b, SeverityWarning, fmt.Sprintf("transport failure on %v; backend unreachable", transport))
	default:
		return cc.result(b, SeverityOK, fmt.Sprintf("verified: %v", verified))
	}
}

// clockSkewWarnThreshold is the absolute drift between the local
// clock and the backend's `Date` header at which ClockSkewChecker
// downgrades from OK to Warning. 30 seconds is loose enough to
// tolerate ordinary NTP wander and a network round trip, tight
// enough to catch a backend with a broken clock — beyond a few
// seconds, alert timestamps on the wire start lying.
const clockSkewWarnThreshold = 30 * time.Second

// ClockSkewChecker compares the server's `Date` response header
// from /api/v2/status against the local clock. Behaviour:
//
//   - within 30s either direction → SeverityOK.
//   - outside 30s → SeverityWarning with the signed drift in the
//     message ("server is 45s behind", "server is 1m12s ahead").
//   - missing Date header (ErrNoDateHeader) → SeverityOK with a
//     "skipped: no Date header" message. The check could not run,
//     but the absence of a server timestamp is not itself a
//     warning. Tracked as OK rather than introducing a new
//     "skipped" Severity for one case.
//   - any other error → SeverityWarning. The reachability and auth
//     checks already report transport-level failures; surfacing
//     the same root cause as Warning here avoids double-counting.
type ClockSkewChecker struct {
	// now is the test seam. Nil delegates to time.Now.
	now func() time.Time
}

func (ClockSkewChecker) Name() string { return "clock-skew" }

// result stamps a Result with the backend name and this checker's
// Name(), leaving Run to decide only severity and message.
func (cs ClockSkewChecker) result(b config.Backend, sev Severity, msg string) Result {
	return Result{Backend: b.Name, Check: cs.Name(), Severity: sev, Message: msg}
}

func (cs ClockSkewChecker) Run(ctx context.Context, b config.Backend, c backend.Client) Result {
	if c == nil {
		return cs.result(b, SeverityError, "client construction failed at startup")
	}
	prober, ok := c.(backend.Prober)
	if !ok {
		return cs.result(b, SeverityWarning, "client does not implement Prober — skipping")
	}
	now := cs.now
	if now == nil {
		now = time.Now
	}

	serverNow, err := prober.ProbeReadyAt(ctx)
	if errors.Is(err, backend.ErrNoDateHeader) {
		return cs.result(b, SeverityOK, "skipped: no Date header on /api/v2/status")
	}
	if err != nil {
		return cs.result(b, SeverityWarning, fmt.Sprintf("probe failed: %s", err))
	}

	skew := serverNow.Sub(now())
	abs := skew
	if abs < 0 {
		abs = -abs
	}
	if abs <= clockSkewWarnThreshold {
		return cs.result(b, SeverityOK,
			fmt.Sprintf("skew %s within %s threshold", skew.Round(time.Second), clockSkewWarnThreshold))
	}
	direction := "ahead of"
	if skew < 0 {
		direction = "behind"
	}
	return cs.result(b, SeverityWarning,
		fmt.Sprintf("server is %s %s local clock", abs.Round(time.Second), direction))
}

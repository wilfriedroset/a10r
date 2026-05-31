// SPDX-License-Identifier: Apache-2.0

package backend

import "errors"

// Sentinel errors define the five "interesting" failure modes the
// rest of the codebase branches on. Other failure types flow
// through as plain errors and the caller treats them as one-shot
// operational issues.
var (
	// ErrUnsupported signals that the requested operation is not
	// available on this backend (capability flag off, or vanilla
	// Alertmanager being asked for a Mimir-only endpoint). The TUI
	// hides the menu item; callers should not retry.
	ErrUnsupported = errors.New("operation not supported by backend capabilities")

	// ErrUnauthorized signals an authentication failure (401, 403,
	// or proxy-rejected request). Header indicator goes red, flash
	// fires once on the transition; no automatic retry until the
	// config is reloaded.
	ErrUnauthorized = errors.New("authentication failed")

	// ErrUnreachable signals that the backend cannot be contacted at
	// all (connection refused, DNS failure, transport timeout). This
	// is the only sentinel that is implicitly Retryable.
	ErrUnreachable = errors.New("backend unreachable")

	// ErrNotFound signals that the server returned 404 on the
	// requested resource. The vanilla classifier wraps every
	// non-transient 404 with this sentinel so callers can branch on
	// `errors.Is` rather than substring-matching status codes —
	// e.g. the doctor AuthChecker's prefix-probe downgrade gates on
	// this so a 422/400/etc. semantic error cannot accidentally
	// trigger a "set prefix:" claim doctor did not verify
	// (ADR 0039).
	ErrNotFound = errors.New("not found")

	// ErrNoDateHeader is returned by Prober.ProbeReadyAt when the
	// response carries no `Date` header (or the header value fails
	// to parse). The doctor clock-skew check converts this into a
	// Skipped row — the absence of a server timestamp is not a
	// warning, just an observation that the check cannot run.
	ErrNoDateHeader = errors.New("response has no parseable Date header")
)

// Retryabler is the duck-typed interface a concrete error may
// implement to opt into the transport backoff loop. The name follows
// the stdlib convention of suffixing capability interfaces with the
// behaviour they describe (cf. net.Error's Timeout()/Temporary()).
// Callers query via Retryable() below rather than asserting against
// this interface directly.
type Retryabler interface {
	Retryable() bool
}

// Retryable reports whether err signals a transient failure that
// the transport backoff loop should retry. The rules:
//
//   - nil → false.
//   - errors.Is(err, ErrUnreachable) → true.
//   - errors.Is(err, ErrUnauthorized) or errors.Is(err, ErrUnsupported)
//     → false (no amount of retry fixes auth or capability).
//   - any other error implementing `Retryable() bool` → that value.
//   - everything else → false (default to "do not retry" so a
//     misclassified error does not loop forever).
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnreachable) {
		return true
	}
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrUnsupported) {
		return false
	}
	var r Retryabler
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return false
}

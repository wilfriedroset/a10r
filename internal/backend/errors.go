// SPDX-License-Identifier: Apache-2.0

package backend

import "errors"

// Sentinel errors are the five "interesting" failure modes the rest of
// the codebase branches on; other failures flow through as plain errors.
var (
	// ErrUnsupported: operation off on this backend (capability flag off,
	// or vanilla Alertmanager asked for a Mimir-only endpoint). The TUI
	// hides the menu item; callers should not retry.
	ErrUnsupported = errors.New("operation not supported by backend capabilities")

	// ErrUnauthorized: auth failure (401, 403, or proxy-rejected). Header
	// indicator goes red, flash fires once on the transition; no auto
	// retry until the config is reloaded.
	ErrUnauthorized = errors.New("authentication failed")

	// ErrUnreachable: backend cannot be contacted at all (conn refused,
	// DNS failure, transport timeout). The only sentinel implicitly Retryable.
	ErrUnreachable = errors.New("backend unreachable")

	// ErrNotFound: server returned 404. The vanilla classifier wraps every
	// non-transient 404 so the doctor AuthChecker's prefix-probe downgrade
	// gates on errors.Is here — a 422/400 semantic error must not trigger a
	// "set prefix:" claim doctor did not verify (ADR 0039).
	ErrNotFound = errors.New("not found")

	// ErrNoDateHeader: Prober.ProbeReadyAt got no parseable Date header.
	// The doctor clock-skew check renders a Skipped row — absence of a
	// server timestamp is an observation, not a warning.
	ErrNoDateHeader = errors.New("response has no parseable Date header")
)

// Retryabler is the duck-typed interface a concrete error implements to
// opt into the transport backoff loop; callers query via Retryable() below.
type Retryabler interface {
	Retryable() bool
}

// Retryable reports whether the transport backoff loop should retry err,
// defaulting to "do not retry" so a misclassified error cannot loop forever.
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

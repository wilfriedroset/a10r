// SPDX-License-Identifier: Apache-2.0

package cmd

// Exit codes returned by the a10r CLI. Stable contract per ADR 0009 and
// docs/end-users/exit-codes.md; CI wrappers branch on these. Append new
// codes, never insert between existing values — wrapper scripts are
// consumers. Per-backend partial failures exit ExitOK with stderr warnings.
const (
	ExitOK = 0

	// ExitRuntimeError is cobra's default exit for any non-typed error.
	ExitRuntimeError = 1

	// ExitConfigInvalid means a10r.yaml failed to parse or validate.
	ExitConfigInvalid = 2

	// ExitUnreachable means every backend in scope failed at the network level.
	ExitUnreachable = 3

	// ExitAuthFailed means every backend in scope rejected credentials (401/403).
	ExitAuthFailed = 4

	// ExitFailMatched means --fail was set and at least one row matched, so
	// on-call wrappers can page without parsing output.
	ExitFailMatched = 10
)

// ExitError wraps an error with an exit code for main() to translate via
// errors.As. Plain errors fall through to ExitRuntimeError. Keeps RunE local
// (no threaded exit-code param) while main owns the os.Exit call.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap exposes the wrapped error so callers can match both ExitError
// (for the code) and any sentinel further down the chain.
func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewExitError wraps a failure mode in an exit code. nil err returns nil so
// callers can compose without an if-nil dance.
func NewExitError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: code, Err: err}
}

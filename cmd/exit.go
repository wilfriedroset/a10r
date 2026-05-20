// SPDX-License-Identifier: Apache-2.0

package cmd

// Exit codes returned by the a10r CLI. Documented as a stable
// contract in docs/end-users/exit-codes.md and ADR 0009; CI/CD
// wrappers branch on these values to distinguish remediation
// types (fix-credentials vs fix-network vs fix-config) without
// having to parse stderr.
//
// A future code MUST be appended (never inserted between
// existing values) — every wrapper script in a user's CI is a
// consumer.
const (
	// ExitOK reports success. main() returns 0 when cmd.Execute
	// returns nil.
	ExitOK = 0

	// ExitRuntimeError is the catch-all for unexpected failures
	// that do not fit any of the more specific buckets below.
	// cobra's default exit on a non-typed error.
	ExitRuntimeError = 1

	// ExitConfigInvalid signals that a10r.yaml could not be
	// parsed or failed schema validation. Operator action: fix
	// the config file.
	ExitConfigInvalid = 2

	// ExitUnreachable signals that every backend in the active
	// scope failed to respond at the network level (DNS,
	// timeout, connection refused). Operator action: fix
	// network connectivity. Partial failures (some tenants ok,
	// some unreachable) exit ExitOK with stderr warnings — see
	// ADR 0009.
	ExitUnreachable = 3

	// ExitAuthFailed signals that every backend in the active
	// scope rejected the configured credentials with 401/403.
	// Operator action: fix credentials. Same partial-failure
	// rule as ExitUnreachable.
	ExitAuthFailed = 4

	// ExitFailMatched is returned when --fail is set on a
	// list-style command (alerts list, silences list) and at
	// least one row matched the filter. Lets on-call wrappers
	// do `a10r alerts list --severity=critical --fail ||
	// page-oncall` without parsing the output.
	ExitFailMatched = 10
)

// ExitError wraps an underlying error with a specific exit code
// for main() to translate. Subcommands return one of these
// instead of a plain error when the failure mode maps onto a
// table entry above; main() type-switches on it via errors.As.
//
// Plain (non-ExitError) errors fall through to ExitRuntimeError.
// This shape keeps the per-subcommand RunE local — they don't
// have to thread an exit-code parameter through helpers — while
// still letting main own the os.Exit call.
type ExitError struct {
	// Code is one of the Exit* constants and becomes the process
	// exit status when main() type-switches on this error.
	Code int
	// Err is the underlying error returned by the subcommand; its
	// Error() text is what stderr-formatting paths render.
	Err error
}

// Error implements the error interface, delegating to the
// wrapped Err so existing fmt-based formatting paths render the
// underlying message unchanged.
func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap exposes the wrapped error for errors.Is / errors.As
// chains. Lets a caller match on both ExitError (for the code)
// and any sentinel further down (for context-specific
// handling).
func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewExitError is the constructor subcommands call when wrapping
// a more-specific failure mode. nil err returns nil so callers
// can compose without if-nil dance:
//
//	return cmd.NewExitError(cmd.ExitConfigInvalid, validate(cfg))
func NewExitError(code int, err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: code, Err: err}
}

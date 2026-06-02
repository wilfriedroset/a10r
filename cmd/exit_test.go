// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExitError_Error_DelegatesToWrapped(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("oops")
	ee := &ExitError{Code: ExitConfigInvalid, Err: wrapped}
	require.Equal(t, "oops", ee.Error())
}

func TestExitError_Error_NilSafe(t *testing.T) {
	t.Parallel()

	var ee *ExitError
	require.Empty(t, ee.Error(), "nil receiver returns empty string")

	ee = &ExitError{Code: ExitRuntimeError, Err: nil}
	require.Empty(t, ee.Error(), "nil wrapped error returns empty string")
}

func TestExitError_Unwrap(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("oops")
	ee := &ExitError{Code: ExitConfigInvalid, Err: wrapped}
	require.Same(t, wrapped, ee.Unwrap())

	var nilEE *ExitError
	require.NoError(t, nilEE.Unwrap())
}

func TestExitError_ErrorsAs(t *testing.T) {
	t.Parallel()

	wrapped := errors.New("validation failed")
	err := NewExitError(ExitConfigInvalid, wrapped)

	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
}

func TestExitError_ErrorsIs_PassesThroughWrapped(t *testing.T) {
	t.Parallel()

	// errors.Is walks through Unwrap, so a sentinel further down
	// the chain stays matchable from outside the ExitError shell.
	sentinel := errors.New("sentinel")
	wrapped := &wrappedError{msg: "outer", inner: sentinel}
	err := NewExitError(ExitRuntimeError, wrapped)

	require.ErrorIs(t, err, sentinel,
		"errors.Is must walk through ExitError to find sentinel")
}

// wrappedError is a tiny wrap-with-Unwrap helper for the
// errors.Is chain test. Testing-only.
type wrappedError struct {
	msg   string
	inner error
}

func (w *wrappedError) Error() string { return w.msg + ": " + w.inner.Error() }
func (w *wrappedError) Unwrap() error { return w.inner }

func TestNewExitError_NilErrorReturnsNil(t *testing.T) {
	t.Parallel()

	require.NoError(t, NewExitError(ExitConfigInvalid, nil),
		"nil err short-circuits so callers don't have to if-nil")
}

func TestExitCodes_StableValues(t *testing.T) {
	t.Parallel()

	// ADR 0009 + docs/end-users/exit-codes.md document these
	// values as a stable contract. Pin them in the test so a
	// future renumber surfaces here loud.
	require.Equal(t, 0, ExitOK)
	require.Equal(t, 1, ExitRuntimeError)
	require.Equal(t, 2, ExitConfigInvalid)
	require.Equal(t, 3, ExitUnreachable)
	require.Equal(t, 4, ExitAuthFailed)
	require.Equal(t, 10, ExitFailMatched)
}

// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSentinels_AreDistinct(t *testing.T) {
	t.Parallel()

	// Each sentinel must be its own value so callers can branch.
	require.NotErrorIs(t, ErrUnsupported, ErrUnauthorized)
	require.NotErrorIs(t, ErrUnsupported, ErrUnreachable)
	require.NotErrorIs(t, ErrUnauthorized, ErrUnreachable)
}

func TestSentinels_SurviveWrap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		wrap   error
		target error
	}{
		{name: "wrapped unsupported", wrap: fmt.Errorf("foo: %w", ErrUnsupported), target: ErrUnsupported},
		{name: "wrapped unauthorized", wrap: fmt.Errorf("foo: %w", ErrUnauthorized), target: ErrUnauthorized},
		{name: "wrapped unreachable", wrap: fmt.Errorf("foo: %w", ErrUnreachable), target: ErrUnreachable},
		{
			name:   "double-wrapped unreachable",
			wrap:   fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrUnreachable)),
			target: ErrUnreachable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.wrap, tc.target)
		})
	}
}

func TestRetryable_NilReturnsFalse(t *testing.T) {
	t.Parallel()
	require.False(t, Retryable(nil))
}

func TestRetryable_UnreachableIsRetryable(t *testing.T) {
	t.Parallel()
	require.True(t, Retryable(ErrUnreachable))
	require.True(t, Retryable(fmt.Errorf("connection refused: %w", ErrUnreachable)))
}

func TestRetryable_UnauthorizedAndUnsupportedAreNotRetryable(t *testing.T) {
	t.Parallel()
	require.False(t, Retryable(ErrUnauthorized))
	require.False(t, Retryable(ErrUnsupported))
	require.False(t, Retryable(fmt.Errorf("wrap: %w", ErrUnauthorized)))
	require.False(t, Retryable(fmt.Errorf("wrap: %w", ErrUnsupported)))
}

func TestRetryable_HonoursOptInOnConcreteErrors(t *testing.T) {
	t.Parallel()

	// Concrete impls (e.g. a 5xx HTTP error in #11) opt into the C1
	// backoff loop by implementing Retryable() bool. errors.As walks
	// the wrap chain so a wrapped instance still surfaces.
	require.True(t, Retryable(retryableError{retryable: true}))
	require.False(t, Retryable(retryableError{retryable: false}))
	require.True(t, Retryable(fmt.Errorf("wrap: %w", retryableError{retryable: true})))
}

func TestRetryable_UnknownErrorDefaultsFalse(t *testing.T) {
	t.Parallel()

	// Default-deny for anything we don't recognise. A future error
	// type that should retry must opt in via Retryable() bool; until
	// it does, the C1 loop won't burn cycles on it.
	require.False(t, Retryable(errors.New("some unrelated failure")))
}

// retryableError is a test-only error that opts into the Retryable
// contract via the duck-typed `Retryable() bool` method.
type retryableError struct {
	retryable bool
}

func (r retryableError) Error() string {
	return fmt.Sprintf("retryable=%v", r.retryable)
}

func (r retryableError) Retryable() bool { return r.retryable }

// SPDX-License-Identifier: Apache-2.0

package backend

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestRetryable(t *testing.T) {
	t.Parallel()

	// Default-deny semantics: nil and anything we don't recognise (incl.
	// ErrUnauthorized/ErrUnsupported) returns false. ErrUnreachable
	// retries. Concrete impls opt in via the duck-typed `Retryable()
	// bool` — errors.As walks the wrap chain so wrapped instances still
	// surface. Future retryable error types must implement that contract;
	// until they do, the C1 backoff loop won't burn cycles on them.
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unreachable", err: ErrUnreachable, want: true},
		{name: "wrapped unreachable", err: fmt.Errorf("connection refused: %w", ErrUnreachable), want: true},
		{name: "unauthorized", err: ErrUnauthorized, want: false},
		{name: "unsupported", err: ErrUnsupported, want: false},
		{name: "wrapped unauthorized", err: fmt.Errorf("wrap: %w", ErrUnauthorized), want: false},
		{name: "wrapped unsupported", err: fmt.Errorf("wrap: %w", ErrUnsupported), want: false},
		{name: "opt-in true", err: retryableError{retryable: true}, want: true},
		{name: "opt-in false", err: retryableError{retryable: false}, want: false},
		{name: "wrapped opt-in true", err: fmt.Errorf("wrap: %w", retryableError{retryable: true}), want: true},
		{name: "unknown defaults false", err: errors.New("some unrelated failure"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, Retryable(tc.err))
		})
	}
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

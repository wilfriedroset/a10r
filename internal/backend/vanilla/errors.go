// SPDX-License-Identifier: Apache-2.0

package vanilla

import "fmt"

// transientError wraps an HTTP response that the C1 backoff loop
// should retry — 5xx server errors and 429 (Too Many Requests). Opts
// into the backend.Retryabler contract so backend.Retryable(err)
// returns true for these without callers needing to remember which
// status codes are retryable.
type transientError struct {
	status int
}

func (e *transientError) Error() string {
	return fmt.Sprintf("transient HTTP %d", e.status)
}

func (*transientError) Retryable() bool { return true }

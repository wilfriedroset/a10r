// SPDX-License-Identifier: Apache-2.0

package poll

import "time"

// Clock is the small surface the poller needs from time. Tests
// inject a fake clock so backoff progression and tick scheduling
// can be exercised without wall-clock waits; production wraps
// time directly.
type Clock interface {
	// Now returns the current time. Used by tests to mark the
	// fake-clock's notion of "now"; the production poller does not
	// query it on the hot path.
	Now() time.Time
	// After returns a channel that fires once after d has elapsed.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the production Clock backed by stdlib time.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

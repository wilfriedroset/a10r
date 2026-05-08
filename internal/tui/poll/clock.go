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
	// Retained for tests that don't need timer cancellation.
	After(d time.Duration) <-chan time.Time
	// NewTimer returns a Timer that fires after d, and whose Stop
	// drops it before it fires. The poll loop uses this rather than
	// After so a manual refresh or ctx cancel can release the
	// pending timer instead of leaking it until d elapses — at
	// thousands of refreshes across a multi-day session, the leaked
	// timers would otherwise pile up in the runtime's heap.
	NewTimer(d time.Duration) Timer
}

// Timer is a stoppable one-shot timer. Mirrors the relevant subset
// of *time.Timer (C and Stop) so the production SystemClock can
// return the stdlib Timer directly while fakes can implement
// channel-backed equivalents.
type Timer interface {
	// C returns the timer's fire channel.
	C() <-chan time.Time
	// Stop tries to drop the timer before it fires. Returns true if
	// the call stopped the timer before it expired; false if the
	// timer had already expired or been stopped. Callers that get
	// false should drain C() to release any value already queued.
	Stop() bool
}

// SystemClock is the production Clock backed by stdlib time.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTimer implements Clock.
func (SystemClock) NewTimer(d time.Duration) Timer { return systemTimer{t: time.NewTimer(d)} }

// systemTimer wraps *time.Timer to satisfy the Timer interface.
// Lives here rather than in poll.go so the Clock contract and its
// concrete production impl stay co-located.
type systemTimer struct{ t *time.Timer }

// C implements Timer.
func (s systemTimer) C() <-chan time.Time { return s.t.C }

// Stop implements Timer.
func (s systemTimer) Stop() bool { return s.t.Stop() }

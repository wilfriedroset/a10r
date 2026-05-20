// SPDX-License-Identifier: Apache-2.0

// Package clock holds the time-injection seams every a10r package
// uses to keep tests off the wall clock. See ADR 0031.
package clock

import "time"

// Now is the minimal time-source seam: a single Now() call so tests
// can pin the wall clock without sleeps. Sufficient for cache TTL
// checks, chord deadlines, and anything that only needs to read the
// clock.
type Now interface {
	Now() time.Time
}

// Clock is the richer time-source seam consumed by the poller. Adds
// After + NewTimer so the backoff loop can schedule and (crucially)
// cancel a pending timer when the user manually refreshes — the
// alternative (After-only) leaks one runtime timer per cancelled
// tick, which compounds over a multi-day session.
type Clock interface {
	Now
	After(d time.Duration) <-chan time.Time
	NewTimer(d time.Duration) Timer
}

// Timer is a stoppable one-shot timer. Mirrors the relevant subset
// of *time.Timer (C + Stop) so the production System value can wrap
// stdlib timers directly while test fakes back the channel by hand.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// System is the production implementation backed by stdlib time.
// Satisfies both Now (via the trivial Now method) and Clock (via the
// After + NewTimer methods on the same receiver).
type System struct{}

// Now returns the current local time.
func (System) Now() time.Time { return time.Now() }

// After returns a channel that fires after d.
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTimer returns a stoppable timer that fires after d.
func (System) NewTimer(d time.Duration) Timer { return systemTimer{t: time.NewTimer(d)} }

// systemTimer wraps *time.Timer to satisfy the Timer interface.
// Lives here rather than in the poll consumer so the contract and
// its sanctioned impl stay co-located.
type systemTimer struct{ t *time.Timer }

// C returns the timer's fire channel.
func (s systemTimer) C() <-chan time.Time { return s.t.C }

// Stop tries to drop the timer before it fires. Returns true if the
// call stopped the timer before it expired; false if the timer had
// already expired or been stopped. Callers that get false should
// drain C() to release any value already queued.
func (s systemTimer) Stop() bool { return s.t.Stop() }

// SPDX-License-Identifier: Apache-2.0

package keys

import "time"

// Clock abstracts time so the dispatcher's chord-timing tests can
// drive every branch without sleep. Production wraps stdlib time
// via SystemClock; tests use a deterministic fake clock that
// advances on demand.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production implementation backed by time.Now.
type SystemClock struct{}

// Now returns the current local time.
func (SystemClock) Now() time.Time { return time.Now() }

// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"time"

	"github.com/wilfriedroset/a10r/internal/tui/header"
)

// BackendHealth is the per-tenant transport state behind the error
// band; an entry exists only while the tenant is not connected.
// Mirrors poll.BackendStatusMsg, translated at the Base seam so
// poller-schema changes don't ripple through every page. Failures
// and NextAt are reserved for future consumers (header tooltip,
// doctor).
type BackendHealth struct {
	State    header.ConnState
	Detail   string
	Failures int
	NextAt   time.Time
}

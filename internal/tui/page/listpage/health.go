// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"time"

	"github.com/wilfriedroset/a10r/internal/tui/header"
)

// BackendHealth is the per-tenant transport state a list page holds
// to render the error band. An entry exists only while the tenant
// is not connected; HandleBackendStatusMsg clears the entry on
// recovery. Fields mirror the wire poll.BackendStatusMsg payload —
// the wire→domain translation lives at the Base seam so future
// poller-schema changes don't ripple through every page's mirror.
// State is the canonical connected/degraded/unreachable trichotomy
// (header.ConnState); Detail carries the operator-facing message;
// Failures and NextAt expose the running counter and the clock the
// poller will next attempt at, reserved for future consumers
// (header tooltip, doctor).
type BackendHealth struct {
	State    header.ConnState
	Detail   string
	Failures int
	NextAt   time.Time
}

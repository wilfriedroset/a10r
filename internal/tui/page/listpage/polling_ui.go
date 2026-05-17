// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"time"

	"charm.land/bubbles/v2/spinner"
)

// PollingUI holds the per-page polling-feedback state. Embedded by
// list pages that present a refresh UI (alerts, silences, groups);
// NOT embedded by receivers because receivers has no manual refresh,
// no spinner-during-refresh, and no per-tenant refresh display. See
// ADR 0013 for the rationale on splitting this off from Base.
//
// Fields are exported for the same cross-package promotion reason
// as Base: embedders in sibling packages cannot access unexported
// fields via promotion.
type PollingUI struct {
	Refreshing    bool
	PausedRefresh bool
	PolledTenants map[string]struct{}
	NextRefresh   map[string]time.Time
	Spinner       spinner.Model
}

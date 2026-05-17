// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"fmt"
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

// NextRefreshLabel formats the bottom-border deadline used by the
// list pages' Footer ("next refresh 25s"). Past-due renders as
// "due" so a slow tick reads honestly without flashing a negative
// duration. Pure helper — kept in the listpage package because it
// only makes sense for pages that present a refresh UI.
func NextRefreshLabel(now, next time.Time) string {
	d := next.Sub(now)
	if d <= 0 {
		return "due"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

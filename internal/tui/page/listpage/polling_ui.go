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
	// Refreshing is true between an `r` press and the next in-scope
	// poll.DataMsg arrival so the renderer keeps the spinner up
	// while the caller's nudge is in flight. Cleared only on the
	// first in-scope DataMsg afterward.
	Refreshing bool
	// PausedRefresh, when true, signals "the next DataMsg is from
	// an explicit r-press; honour it even though paused". Cleared
	// after the first DataMsg consumes it — lets the operator hold
	// pause but pull a single fresh snapshot on demand.
	PausedRefresh bool
	// PolledTenants is the set of tenants that have produced at
	// least one DataMsg in this page's lifetime. Scope-aware so a
	// fast out-of-scope tenant returning [] doesn't flip the page
	// out of loading state before the in-scope tenant has answered.
	PolledTenants map[string]struct{}
	// NextRefresh is the per-tenant DataMsg.NextAt timestamp. The
	// footer collapses it into "next refresh Ns" by picking the
	// soonest entry across in-scope tenants.
	NextRefresh map[string]time.Time
	// Spinner is the cold-start / refresh-in-flight indicator.
	// Stopped (Tick chain broken) outside of those two windows; see
	// each page's spinner.TickMsg branch in Update.
	Spinner spinner.Model
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

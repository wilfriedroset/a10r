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

// PolledInScope reports whether at least one in-scope tenant has
// produced a DataMsg. The scope predicate is supplied by the caller
// (typically `Base.ScopeIncludes`) so PollingUI stays unaware of
// Base's scope/tenants fields — see ADR 0013's inclusion rule.
func (u *PollingUI) PolledInScope(includes func(string) bool) bool {
	for tenant := range u.PolledTenants {
		if includes(tenant) {
			return true
		}
	}
	return false
}

// SpinnerActive reports whether the loading affordance should
// keep ticking — true during the cold-start window (no in-scope
// DataMsg yet) and during a manual `r` refresh in flight. See
// CONTEXT.md for the affordance vocabulary.
func (u *PollingUI) SpinnerActive(includes func(string) bool) bool {
	return !u.PolledInScope(includes) || u.Refreshing
}

// SoonestNextRefresh returns the earliest DataMsg.NextAt among
// in-scope tenants. Zero when no in-scope tenant has published a
// NextAt; the refresh countdown caller renders that case as the
// empty footer branch.
func (u *PollingUI) SoonestNextRefresh(includes func(string) bool) time.Time {
	var soonest time.Time
	for tenant, ts := range u.NextRefresh {
		if !includes(tenant) {
			continue
		}
		if soonest.IsZero() || ts.Before(soonest) {
			soonest = ts
		}
	}
	return soonest
}

// LoadingTitle returns the loading affordance title used while
// the page is in a loading window — `<spinner-frame> loading <noun>…`.
// Pages compose it into their Title() by branching on SpinnerActive.
func (u *PollingUI) LoadingTitle(noun string) string {
	return u.Spinner.View() + " loading " + noun + "…"
}

// RefreshCountdown returns the refresh-countdown footer for a polled
// list page. Five branches, in priority order: paused (with or
// without a manual refresh in flight), refreshing alone, pre-poll
// (no in-scope tenant has answered yet), polled-without-NextAt, and
// polled-with-NextAt (rendered via NextRefreshLabel). See CONTEXT.md
// for the vocabulary.
func RefreshCountdown(paused, refreshing, polled bool, soonest, now time.Time) string {
	if paused {
		if refreshing {
			return "WATCH OFF · refreshing…"
		}
		return "WATCH OFF"
	}
	if refreshing {
		return "refreshing…"
	}
	if !polled || soonest.IsZero() {
		return ""
	}
	return "next refresh " + NextRefreshLabel(now, soonest)
}

// NextRefreshLabel formats the bottom-border deadline used by the
// refresh countdown ("next refresh 25s"). Past-due renders as
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

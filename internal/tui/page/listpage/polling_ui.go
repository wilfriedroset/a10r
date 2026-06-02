// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
)

// PollingUI holds per-page polling-feedback state. Split off Base and
// embedded only by pages with a refresh UI (alerts, silences, groups),
// not receivers, which has no manual refresh — see ADR 0013. Fields
// are exported because sibling-package embedders can't reach
// unexported fields via promotion.
type PollingUI struct {
	// Refreshing stays true between an `r` press and the next
	// in-scope DataMsg so the spinner keeps up while the nudge is in
	// flight.
	Refreshing bool
	// PausedRefresh signals "honour the next DataMsg though paused";
	// consumed by the first DataMsg, so a paused operator can still
	// pull one fresh snapshot.
	PausedRefresh bool
	// PolledTenants is the set of tenants that produced a DataMsg
	// this lifetime. Scope-aware so a fast out-of-scope tenant
	// returning [] doesn't drop the loading state early.
	PolledTenants map[string]struct{}
	// NextRefresh is the per-tenant DataMsg.NextAt; the footer shows
	// the soonest in-scope entry.
	NextRefresh map[string]time.Time
	// Spinner is the cold-start / refresh-in-flight indicator,
	// stopped outside those windows.
	Spinner spinner.Model
}

// PolledInScope reports whether any in-scope tenant has produced a
// DataMsg. The caller supplies the scope predicate (typically
// Base.ScopeIncludes) so PollingUI stays unaware of Base — see
// ADR 0013.
func (u *PollingUI) PolledInScope(includes func(string) bool) bool {
	for tenant := range u.PolledTenants {
		if includes(tenant) {
			return true
		}
	}
	return false
}

// SpinnerActive reports whether the loading affordance should keep
// ticking — during cold start (no in-scope DataMsg yet) or a manual
// refresh in flight. See CONTEXT.md for the vocabulary.
func (u *PollingUI) SpinnerActive(includes func(string) bool) bool {
	return !u.PolledInScope(includes) || u.Refreshing
}

// SoonestNextRefresh returns the earliest in-scope DataMsg.NextAt,
// zero when none has published one (the countdown caller renders that
// as the empty footer branch).
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

// LoadingTitle returns the loading-window title `<frame> loading
// <noun>…`, composed into Title() by branching on SpinnerActive.
func (u *PollingUI) LoadingTitle(noun string) string {
	return u.Spinner.View() + " loading " + noun + "…"
}

// RefreshCountdown returns the refresh-countdown footer, branching in
// priority order: paused, refreshing, pre-poll, polled-without-NextAt,
// polled-with-NextAt. See CONTEXT.md for the vocabulary.
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

// NextRefreshLabel formats the countdown deadline ("25s"). Past-due
// renders "due" so a slow tick reads honestly instead of flashing a
// negative duration.
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

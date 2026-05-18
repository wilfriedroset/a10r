// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// ApplyDataMsg runs the success-path ritual every polled list page
// duplicates today: validate the tenant, gate on pause + watch-
// mode escape hatch, write the typed payload through the page-
// provided store closure, capture poll metadata (NextRefresh +
// PolledTenants), drop the spinner once an in-scope tenant has
// answered, and trigger the page's Recompute. Returns true when
// the message was claimed (handled or silently dropped).
//
// Generic over the resource type R because each page stores a
// different concrete slice (`[]backend.Alert`, `[]backend.Silence`,
// `[]backend.AlertGroup`). A free function rather than a method on
// Base because Go does not allow generic methods. See ADR-0018.
//
// Panics with a clear message when Base.Recompute is nil — a page
// that ingests data without re-rendering is a wiring bug, not a
// silent-no-op condition.
func ApplyDataMsg[R any](b *Base, u *PollingUI, msg poll.DataMsg, store func(tenant string, payload R)) bool {
	if b.Recompute == nil {
		panic("listpage.ApplyDataMsg: Base.Recompute callback not wired by page constructor")
	}
	r, ok := msg.Resource.(R)
	if !ok {
		return false
	}
	if !b.KnownTenant(msg.Tenant) {
		return true
	}
	if b.Paused && !u.PausedRefresh {
		return true
	}
	u.PausedRefresh = false
	store(msg.Tenant, r)
	if !msg.NextAt.IsZero() {
		u.NextRefresh[msg.Tenant] = msg.NextAt
	}
	u.PolledTenants[msg.Tenant] = struct{}{}
	if b.ScopeIncludes(msg.Tenant) {
		u.Refreshing = false
	}
	b.Recompute()
	return true
}

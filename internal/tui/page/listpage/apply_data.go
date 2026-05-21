// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// ApplyDataMsg implements the success-path ritual every polled
// list page shares. Generic over the resource type R because each
// page stores a different concrete slice; a free function rather
// than a method on Base because Go does not allow generic
// methods. Wrong payload type / unknown tenant / paused-without-
// PausedRefresh leave state unchanged. See ADR-0018.
//
// Panics with a clear message when Base.Recompute is nil — a page
// that ingests data without re-rendering is a wiring bug.
func ApplyDataMsg[R any](b *Base, u *PollingUI, msg poll.DataMsg, store func(tenant string, payload R)) {
	if b.Recompute == nil {
		panic("listpage.ApplyDataMsg: Base.Recompute callback not wired by page constructor")
	}
	r, ok := msg.Resource.(R)
	if !ok {
		return
	}
	if !b.KnownTenant(msg.Tenant) {
		return
	}
	if b.Paused && !u.PausedRefresh {
		return
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
}

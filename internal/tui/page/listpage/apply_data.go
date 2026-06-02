// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// ApplyDataMsg runs the success-path ritual every polled list page
// shares. A free function, not a method, because Go has no generic
// methods and each page stores a different concrete slice R. Wrong
// payload / unknown tenant / paused-without-PausedRefresh leave
// state unchanged. Panics on nil Recompute — ingesting without
// re-rendering is a wiring bug. See ADR-0018.
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

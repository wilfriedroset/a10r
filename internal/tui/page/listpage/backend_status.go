// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// HandleBackendStatusMsg translates poll.BackendStatusMsg into a
// BackendHealth entry, writing or deleting the per-tenant row.
// Shared on Base because all four list pages need the byte-identical
// handler. Unknown tenants are dropped so a wire bug or stale source
// can't pollute the map with names that never poll again (empty
// Tenants disables the guard). Recovery clears the entry so a healed
// tenant drops off the error band rather than lingering stale.
func (b *Base) HandleBackendStatusMsg(m poll.BackendStatusMsg) {
	if !b.KnownTenant(m.Tenant) {
		return
	}
	if m.State == header.ConnConnected {
		delete(b.BackendHealth, m.Tenant)
		return
	}
	b.BackendHealth[m.Tenant] = BackendHealth{
		State:    m.State,
		Detail:   m.Detail,
		Failures: m.Failures,
		NextAt:   m.NextAt,
	}
}

// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// HandleBackendStatusMsg translates the wire-format
// poll.BackendStatusMsg into a BackendHealth entry and writes (or
// deletes) the per-tenant row. The four list pages
// (alerts/silences/groups/receivers) all share this exact handler
// shape, so it lives on Base to collapse the byte-identical
// per-page case blocks into a single call.
//
// Tenants outside the configured list are silently dropped — a
// wire-layer bug, test leak, or future hot-reload that hasn't
// pruned its sources could otherwise pollute BackendHealth with
// names that will never poll again. Empty Tenants disables the
// guard so fixtures without an explicit list keep working.
//
// Recovery (State == ConnConnected) clears the entry so a
// recovered tenant drops off the error band immediately rather
// than lingering with a stale Detail.
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

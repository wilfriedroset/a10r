// SPDX-License-Identifier: Apache-2.0

package listpage

// ShowTenantColumn reports whether the page should render a
// leading TENANT column. True when the active scope is "all" and
// the configured tenant fleet spans more than one backend — what
// k9s does in its namespace=all view.
//
// "More than one backend" means CONFIGURED, not "more than one
// has produced data". A broken tenant whose first poll never
// completes (connection refused) must still count, otherwise the
// column auto-hides on a two-backend fleet the moment one of
// them goes down — exactly the moment the operator needs the
// column to spot which backend is missing. observed is the
// fall-through used when no configured list was plumbed in: the
// page passes the count of tenants that have produced data so
// tests that don't pin Tenants keep working.
func (b *Base) ShowTenantColumn(observed int) bool {
	if b.Scope != scopeAll {
		return false
	}
	if n := len(b.Tenants); n > 0 {
		return n > 1
	}
	return observed > 1
}

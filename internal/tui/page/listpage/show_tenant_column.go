// SPDX-License-Identifier: Apache-2.0

package listpage

// ShowTenantColumn reports whether the page should render a
// leading TENANT column. True when the active scope is "all" and
// the configured tenant fleet spans more than one backend —
// mirroring k9s's namespace=all view.
//
// The configured count is preferred over the observed count so a
// broken tenant (connection refused at first poll) still keeps
// the column visible — exactly the moment the operator needs it
// to spot the missing backend. observed is the fall-through when
// no configured list was plumbed in (test paths that don't pin
// Tenants).
func (b *Base) ShowTenantColumn(observed int) bool {
	if b.Scope != scopeAll {
		return false
	}
	if n := len(b.Tenants); n > 0 {
		return n > 1
	}
	return observed > 1
}

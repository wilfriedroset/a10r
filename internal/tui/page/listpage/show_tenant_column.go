// SPDX-License-Identifier: Apache-2.0

package listpage

// ShowTenantColumn reports whether to render a leading TENANT
// column: scope "all" over a >1-backend fleet, mirroring k9s's
// namespace=all view. Configured count beats observed so a broken
// tenant still keeps the column visible — the moment the operator
// needs it to spot the missing backend. observed is the fall-through
// when no configured list was plumbed in.
func (b *Base) ShowTenantColumn(observed int) bool {
	if b.Scope != ScopeAll {
		return false
	}
	if n := len(b.Tenants); n > 0 {
		return n > 1
	}
	return observed > 1
}

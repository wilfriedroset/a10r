// SPDX-License-Identifier: Apache-2.0

package listpage

import "slices"

// KnownTenant reports whether name is in b.Tenants, used to gate
// incoming DataMsg / BackendStatusMsg state mutations. An empty
// configured list disables the guard so test fixtures that don't
// pin Tenants on the page (or legacy upstream wiring with no
// canonical list) keep working.
func (b *Base) KnownTenant(name string) bool {
	if len(b.Tenants) == 0 {
		return true
	}
	return slices.Contains(b.Tenants, name)
}

// SPDX-License-Identifier: Apache-2.0

package listpage

import "slices"

// KnownTenant reports whether name is in b.Tenants, gating DataMsg /
// BackendStatusMsg mutations. An empty list disables the guard so
// fixtures that don't pin Tenants keep working.
func (b *Base) KnownTenant(name string) bool {
	if len(b.Tenants) == 0 {
		return true
	}
	return slices.Contains(b.Tenants, name)
}

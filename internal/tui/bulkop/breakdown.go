// SPDX-License-Identifier: Apache-2.0

package bulkop

import (
	"fmt"
	"sort"
	"strings"
)

// FormatTenantBreakdown returns a stable, human-readable summary of
// the tenant distribution across the supplied rows. Single-tenant
// inputs return the bare tenant name; multi-tenant inputs return
// alphabetically sorted "tenant=count" pairs joined by ", ". Used by
// bulk-op confirmation messaging so the operator sees how requests
// fan out per backend before committing.
func FormatTenantBreakdown[E any](items []E, tenantFn func(E) string) string {
	counts := map[string]int{}
	tenants := []string{}
	for _, it := range items {
		t := tenantFn(it)
		if _, seen := counts[t]; !seen {
			tenants = append(tenants, t)
		}
		counts[t]++
	}
	sort.Strings(tenants)
	if len(tenants) == 1 {
		return tenants[0]
	}
	parts := make([]string, len(tenants))
	for i, t := range tenants {
		parts[i] = fmt.Sprintf("%s=%d", t, counts[t])
	}
	return strings.Join(parts, ", ")
}

// SPDX-License-Identifier: Apache-2.0

package listpage

import "strings"

// ScopeAll is the canonical "every configured tenant" scope label.
const ScopeAll = "all"

// ScopeIncludes reports whether tenant is in b.Scope. Empty / "all"
// includes everyone; otherwise b.Scope is a comma-joined list (a
// Ctrl+T multi-select like "prod,staging"), whitespace-trimmed per
// entry so a pasted scope still matches.
func (b *Base) ScopeIncludes(tenant string) bool {
	scope := strings.TrimSpace(b.Scope)
	if scope == "" || scope == ScopeAll {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == tenant {
			return true
		}
	}
	return false
}

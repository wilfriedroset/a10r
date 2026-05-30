// SPDX-License-Identifier: Apache-2.0

package listpage

import "strings"

// ScopeAll is the canonical "every configured tenant" scope label.
const ScopeAll = "all"

// ScopeIncludes reports whether tenant should appear in the view
// given b.Scope. Empty / "all" includes everyone; otherwise the
// scope is parsed as a comma-joined list (so a Ctrl+T multi-select
// like "prod,staging" lights up both backends). Trims whitespace
// around each entry so a pasted scope with spaces still matches.
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

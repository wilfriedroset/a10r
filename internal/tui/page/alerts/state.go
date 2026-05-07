// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

// totalAlerts is the unfiltered alert count within the current
// scope. Used by Title (for the [N] suffix) and by the empty-
// state hint (which differentiates "no alerts polled yet" from
// "no alerts match the active filter"). Honours scope so a
// `<1>` quick-switch updates the title's [N] to that tenant's
// alert count rather than the cross-tenant total.
func (p *Page) totalAlerts() int {
	n := 0
	for tenant, alerts := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		n += len(alerts)
	}
	return n
}

// showTenantColumn reports whether the page should render a
// leading TENANT column. True when the active scope spans more
// than one backend's data — which is what k9s does in its
// namespace=all view.
func (p *Page) showTenantColumn() bool {
	return p.scope == scopeAll && len(p.byTenant) > 1
}

// scopeIncludes reports whether tenant should appear in the
// view given p.scope. "all" / empty includes everyone;
// otherwise the scope is parsed as a comma-joined list (so a
// Ctrl+T multi-select like "prod,staging" lights up both
// backends). Mirror of the silences-page predicate so the two
// list pages agree on scope shape.
func (p *Page) scopeIncludes(tenant string) bool {
	scope := strings.TrimSpace(p.scope)
	if scope == "" || scope == scopeAll {
		return true
	}
	for s := range strings.SplitSeq(scope, ",") {
		if strings.TrimSpace(s) == tenant {
			return true
		}
	}
	return false
}

// recompute rebuilds p.view from byTenant, applying scope, state
// filter, substring filter, and the active sort. Called on every
// data / scope / filter / sort change; cheap relative to the
// poll cadence (O(N log N) on hundreds of alerts).
func (p *Page) recompute() {
	defer p.recomputeScroll()
	flat := make([]alertEntry, 0)
	for tenant, alerts := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, a := range alerts {
			flat = append(flat, alertEntry{a: a, tenant: tenant})
		}
	}
	p.view = filterEntries(flat, p.filter, p.stateFilter)
	p.sorter.Apply(p.view)

	// Resolve cursor by fingerprint when we have one to follow.
	if p.focusFingerprint != "" {
		for i, e := range p.view {
			if e.a.Fingerprint == p.focusFingerprint {
				p.cursor = i
				return
			}
		}
	}
	if p.cursor >= len(p.view) {
		p.cursor = max(len(p.view)-1, 0)
	}
	p.snapshotFocus()
}

// recomputeScroll re-aligns p.topRow with p.cursor for the cached
// body height. Called from every state mutation that can move the
// cursor or change len(p.view) so View can read p.topRow without
// reconciling — keeps the render path side-effect-free as long as
// bodyHeight hasn't changed since the last paint. View itself also
// calls this as a backstop for the chrome-resize case where bodyHeight
// shifts between frames without a cursor mutation.
func (p *Page) recomputeScroll() {
	if p.bodyHeight <= 0 {
		return
	}
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, p.bodyHeight, len(p.view))
}

// snapshotFocus captures the fingerprint of the row currently
// under the cursor so subsequent recomputes can re-resolve it.
// Empty view leaves focus empty.
func (p *Page) snapshotFocus() {
	if p.cursor < len(p.view) {
		p.focusFingerprint = p.view[p.cursor].a.Fingerprint
		return
	}
	p.focusFingerprint = ""
}

// cycleStateFilter walks "" → active → suppressed → unprocessed → ""
// per the `t` binding's intent (cycle through state filters).
func (p *Page) cycleStateFilter() {
	cycle := []string{"", string(backend.AlertStateActive), string(backend.AlertStateSuppressed), string(backend.AlertStateUnprocessed)}
	for i, v := range cycle {
		if v == p.stateFilter {
			p.stateFilter = cycle[(i+1)%len(cycle)]
			return
		}
	}
	p.stateFilter = ""
}

// filterEntries returns a new slice containing only entries
// whose Alert matches both the substring and state filters.
// Substring is matched case-insensitively against label and
// annotation values.
func filterEntries(in []alertEntry, substr, state string) []alertEntry {
	if substr == "" && state == "" {
		out := make([]alertEntry, len(in))
		copy(out, in)
		return out
	}
	needle := strings.ToLower(substr)
	out := make([]alertEntry, 0, len(in))
	for _, e := range in {
		if state != "" && string(e.a.State) != state {
			continue
		}
		if substr != "" && !alertMatchesSubstr(e.a, needle) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// alertMatchesSubstr reports whether the needle (already
// lower-cased by the caller) appears in any of a's label values
// or annotation values. Annotations cover summary / description so
// a "high cpu" filter hits an alert whose annotation contains
// "High CPU usage" even when the alertname itself is opaque.
func alertMatchesSubstr(a backend.Alert, needle string) bool {
	for _, v := range a.Labels {
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	for _, v := range a.Annotations {
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	return false
}

// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
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
		if !p.ScopeIncludes(tenant) {
			continue
		}
		n += len(alerts)
	}
	return n
}

// recompute rebuilds p.view from byTenant, applying scope, state
// filter, substring filter, and the active sort. Called on every
// data / scope / filter / sort change; cheap relative to the
// poll cadence (O(N log N) on hundreds of alerts).
func (p *Page) recompute() {
	total, knownFP := p.scanScope()
	flat := p.flatten(total)
	p.view = filterEntries(flat, p.Filter, p.stateFilter)
	p.sorter.Apply(p.view)
	p.resolveFocus(knownFP)
	p.Clamp(len(p.view))
	p.snapshotFocus()
}

// scanScope walks byTenant once and returns the in-scope alert
// count plus whether ANY tenant (including out-of-scope ones)
// still knows about the focused fingerprint. Scanning out-of-scope
// tenants matters so a scope-narrowed-out alert still counts as
// "source knows about this" — keeps the fingerprint anchored
// across a scope switch back. Single pass over the same map we'd
// already walk for `total`.
func (p *Page) scanScope() (total int, knownFP bool) {
	for tenant, alerts := range p.byTenant {
		if p.ScopeIncludes(tenant) {
			total += len(alerts)
		}
		if p.focusFingerprint != "" && !knownFP {
			for _, a := range alerts {
				if a.Fingerprint == p.focusFingerprint {
					knownFP = true
					break
				}
			}
		}
	}
	return total, knownFP
}

// flatten builds the alertEntry slice for in-scope tenants, sized
// from the pre-scanned total so the append loop allocates once.
func (p *Page) flatten(total int) []alertEntry {
	flat := make([]alertEntry, 0, total)
	for tenant, alerts := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		for _, a := range alerts {
			flat = append(flat, alertEntry{
				a:              a,
				tenant:         tenant,
				lowerComposite: alertLowerComposite(a),
			})
		}
	}
	return flat
}

// resolveFocus anchors the cursor on the focused fingerprint when
// the alert is still in view. Two miss-shapes: (a) filter/scope
// narrowed it out but knownFP is true — keep the fingerprint so a
// later widening re-anchors; (b) every tenant's snapshot has
// dropped it — clear so a later widening does not chase a phantom.
func (p *Page) resolveFocus(knownFP bool) {
	if p.focusFingerprint == "" {
		return
	}
	for i, e := range p.view {
		if e.a.Fingerprint == p.focusFingerprint {
			p.SetIndex(i, len(p.view))
			return
		}
	}
	if !knownFP {
		p.focusFingerprint = ""
	}
}

// snapshotFocus captures the fingerprint of the row currently
// under the cursor so subsequent recomputes can re-resolve it.
// Empty view PRESERVES the previous focusFingerprint so a later
// filter-clear (or fresh poll that restores the alert) can re-
// anchor on the originally focused alert. Without this, narrowing
// the filter to zero results and then clearing it would land the
// cursor on row 0 instead of the user's prior position — silent
// loss-of-place that violates the cursor-by-id contract.
func (p *Page) snapshotFocus() {
	if p.Index() < len(p.view) {
		p.focusFingerprint = p.view[p.Index()].a.Fingerprint
	}
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
// whose Alert matches both the search and state filters. The
// search string runs through footer.NewMatcher so a leading `~`
// flips to fuzzy, a leading `\` to literal substring, and a body
// with two distinct regex metas to compiled regex — matching the
// keybindings.md /-prompt contract.
func filterEntries(in []alertEntry, search, state string) []alertEntry {
	matcher := footer.NewMatcher(search)
	if matcher.MatchAll() && state == "" {
		// Caller's `flat` slice is local to recompute and assigned
		// straight to p.view; sharing the backing avoids an O(N)
		// copy that fires every poll tick.
		return in
	}
	out := make([]alertEntry, 0, len(in))
	for _, e := range in {
		if state != "" && string(e.a.State) != state {
			continue
		}
		if !matcher.MatchAll() && !matcher.Match(e.lowerComposite) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// alertLowerComposite concatenates the lower-cased label values
// and annotation values into a single string. Annotations cover
// summary / description so a "high cpu" filter hits an alert whose
// annotation contains "High CPU usage" even when the alertname
// itself is opaque. NUL-separated so a query can't accidentally
// span field boundaries.
func alertLowerComposite(a backend.Alert) string {
	var b strings.Builder
	estimate := 0
	for _, v := range a.Labels {
		estimate += len(v) + 1
	}
	for _, v := range a.Annotations {
		estimate += len(v) + 1
	}
	b.Grow(estimate)
	first := true
	for _, v := range a.Labels {
		if !first {
			b.WriteByte(0)
		}
		first = false
		b.WriteString(strings.ToLower(v))
	}
	for _, v := range a.Annotations {
		if !first {
			b.WriteByte(0)
		}
		first = false
		b.WriteString(strings.ToLower(v))
	}
	return b.String()
}

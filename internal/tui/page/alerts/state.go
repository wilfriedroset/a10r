// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
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
	defer p.recomputeScroll()
	total := 0
	knownFP := false
	for tenant, alerts := range p.byTenant {
		if p.ScopeIncludes(tenant) {
			total += len(alerts)
		}
		// Scan every tenant (not just the in-scope ones) so a scope-
		// narrowed-out alert still counts as "source knows about
		// this" — keeps the fingerprint anchored across a scope
		// switch back. Cheap: same map we iterate for `total`.
		if p.focusFingerprint != "" && !knownFP {
			for _, a := range alerts {
				if a.Fingerprint == p.focusFingerprint {
					knownFP = true
					break
				}
			}
		}
	}
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
	p.view = filterEntries(flat, p.Filter, p.stateFilter)
	p.sorter.Apply(p.view)

	// Resolve cursor by fingerprint when we have one to follow.
	if p.focusFingerprint != "" {
		for i, e := range p.view {
			if e.a.Fingerprint == p.focusFingerprint {
				p.Cursor = i
				return
			}
		}
		// Fell through: the focused alert is not in the view. Two
		// shapes: (a) filter/scope narrowed it out — source still
		// has it, keep focusFingerprint so a later widening re-
		// anchors; (b) alert resolved upstream — every tenant's
		// snapshot has dropped it, clear so a later widening does
		// not chase a phantom.
		if !knownFP {
			p.focusFingerprint = ""
		}
	}
	p.ClampCursor(len(p.view))
	p.snapshotFocus()
}

// recomputeScroll re-aligns p.TopRow with p.Cursor for the cached
// body height. Called from every state mutation that can move the
// cursor or change len(p.view) so View can read p.TopRow without
// reconciling — keeps the render path side-effect-free as long as
// bodyHeight hasn't changed since the last paint. View itself also
// calls this as a backstop for the chrome-resize case where bodyHeight
// shifts between frames without a cursor mutation.
func (p *Page) recomputeScroll() {
	if p.BodyHeight <= 0 {
		return
	}
	p.TopRow = cursor.ReconcileScroll(p.Cursor, p.TopRow, p.BodyHeight, len(p.view))
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
	if p.Cursor < len(p.view) {
		p.focusFingerprint = p.view[p.Cursor].a.Fingerprint
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

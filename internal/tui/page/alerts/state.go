// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"sort"
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// totalGroups is the unfiltered group count within the current
// scope — distinct (tenant, alertname) over every in-scope instance,
// ignoring the substring / state filters. Used by Title for the
// `[viewGroups/totalGroups]` suffix so the denominator reads as "of
// all the alerts in scope" rather than "of the filtered set".
func (p *Page) totalGroups() int {
	seen := map[string]struct{}{}
	for tenant, alerts := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		for _, a := range alerts {
			seen[tenant+"\x00"+a.Labels["alertname"]] = struct{}{}
		}
	}
	return len(seen)
}

// hasInScopeAlerts reports whether any in-scope tenant has at least
// one alert — drives the empty-state hint's "polled, nothing here"
// vs. "filter hides everything" branch.
func (p *Page) hasInScopeAlerts() bool {
	for tenant, alerts := range p.byTenant {
		if p.ScopeIncludes(tenant) && len(alerts) > 0 {
			return true
		}
	}
	return false
}

// recompute rebuilds p.groups from byTenant: scope → flatten to
// per-instance alertEntry → filter (per instance) → aggregate
// survivors into alertGroups → sort. Called on every data / scope /
// filter / sort change; cheap relative to the poll cadence.
func (p *Page) recompute() {
	total, knownKey := p.scanScope()
	flat := p.flatten(total)
	survivors := filterEntries(flat, p.Filter, p.stateFilter)
	p.groups = aggregate(survivors)
	p.sorter.Apply(p.groups)
	p.resolveFocus(knownKey)
	p.Clamp(len(p.groups))
	p.snapshotFocus()
}

// scanScope walks byTenant once and returns the in-scope instance
// count (to size the flatten append) plus whether ANY tenant
// (including out-of-scope ones) still has an instance for the focused
// group key. Scanning out-of-scope tenants keeps a scope-narrowed-out
// group anchored across a scope switch back.
func (p *Page) scanScope() (total int, knownKey bool) {
	for tenant, alerts := range p.byTenant {
		inScope := p.ScopeIncludes(tenant)
		if inScope {
			total += len(alerts)
		}
		if p.focusGroupKey != "" && !knownKey {
			for _, a := range alerts {
				if tenant+"\x00"+a.Labels["alertname"] == p.focusGroupKey {
					knownKey = true
					break
				}
			}
		}
	}
	return total, knownKey
}

// flatten builds the per-instance alertEntry slice for in-scope
// tenants, sized from the pre-scanned total so the append loop
// allocates once. Aggregation happens after filtering, so the filter
// can still operate per instance.
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

// aggregate rolls the post-filter instances up into alertGroups keyed
// by (tenant, alertname). It accumulates count, max severity rank,
// oldest StartsAt, and per-state tallies, then sorts each group's
// instances by fingerprint ASC for a stable drill-down order. The
// returned slice is left unsorted — the caller's sorter orders the
// rows. A missing alertname (Labels["alertname"]=="") groups under
// the synthetic empty-name key; the renderer surfaces it as
// "(no alertname)".
func aggregate(in []alertEntry) []alertGroup {
	byKey := map[string]*alertGroup{}
	order := make([]string, 0)
	for _, e := range in {
		name := e.a.Labels["alertname"]
		key := e.tenant + "\x00" + name
		g, ok := byKey[key]
		if !ok {
			g = &alertGroup{tenant: e.tenant, alertName: name, oldestStart: e.a.StartsAt}
			byKey[key] = g
			order = append(order, key)
		}
		g.instances = append(g.instances, e.a)
		g.count++
		if r := backend.SeverityRank(e.a.Labels); r > g.severityRank {
			g.severityRank = r
		}
		if e.a.StartsAt.Before(g.oldestStart) {
			g.oldestStart = e.a.StartsAt
		}
		switch e.a.State {
		case backend.AlertStateActive:
			g.active++
		case backend.AlertStateSuppressed:
			g.suppressed++
		case backend.AlertStateUnprocessed:
			g.unprocessed++
		}
	}
	out := make([]alertGroup, 0, len(order))
	for _, key := range order {
		g := byKey[key]
		sort.Slice(g.instances, func(i, j int) bool {
			return g.instances[i].Fingerprint < g.instances[j].Fingerprint
		})
		out = append(out, *g)
	}
	return out
}

// resolveFocus anchors the cursor on the focused group key when the
// group is still in view. Two miss-shapes: (a) filter/scope narrowed
// it out but knownKey is true — keep the key so a later widening re-
// anchors; (b) no tenant has any instance for it anymore — clear so a
// later widening does not chase a phantom.
func (p *Page) resolveFocus(knownKey bool) {
	if p.focusGroupKey == "" {
		return
	}
	for i, g := range p.groups {
		if g.key() == p.focusGroupKey {
			p.SetIndex(i, len(p.groups))
			return
		}
	}
	if !knownKey {
		p.focusGroupKey = ""
	}
}

// snapshotFocus captures the group key under the cursor so subsequent
// recomputes can re-resolve it. Empty view PRESERVES the previous key
// so a later filter-clear (or fresh poll that restores the group) re-
// anchors on the originally focused group rather than landing on row 0.
func (p *Page) snapshotFocus() {
	if p.Index() < len(p.groups) {
		p.focusGroupKey = p.groups[p.Index()].key()
	}
}

// cycleStateFilter walks "" → active → suppressed → unprocessed → ""
// per the Shift+F binding's intent (cycle through state filters).
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
		// `in` is recompute's local `flat` slice, consumed only by
		// aggregate() which reads it without retaining it. Returning it
		// unchanged avoids an O(N) copy that would fire every poll tick.
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

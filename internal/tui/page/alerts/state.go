// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/labelcol"
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
			seen[groupKeyOf(tenant, a.Labels[labelAlertname])] = struct{}{}
		}
	}
	return len(seen)
}

// pollDeltaFlash names the aggregates that appeared and disappeared
// for one tenant between two polls. It ignores the `/` filter and the
// state filter on purpose: the flash narrates what the backend did,
// not what the view shows.
func (p *Page) pollDeltaFlash(tenant string, before, after []backend.Alert) tea.Cmd {
	if !p.pollDelta || !p.ScopeIncludes(tenant) {
		return nil
	}
	// A tenant with no map key has never polled, so the whole first
	// snapshot would read as new.
	if _, seen := p.byTenant[tenant]; !seen {
		return nil
	}
	beforeKeys, afterKeys := alertnameKeys(tenant, before), alertnameKeys(tenant, after)
	newCount, resolvedCount := countMissing(afterKeys, beforeKeys), countMissing(beforeKeys, afterKeys)
	if newCount == 0 && resolvedCount == 0 {
		return nil
	}
	text := pollDeltaText(newCount, resolvedCount)
	if p.ScopeTenantCount(len(p.byTenant)) > 1 {
		text = tenant + ": " + text
	}
	level := footer.FlashSuccess
	if newCount > 0 {
		level = footer.FlashWarn
	}
	return footer.ShowWeakFlash(level, text)
}

// alertnameKeys is the aggregate key set of one tenant's snapshot —
// the same (tenant, alertname) identity the table rows on.
func alertnameKeys(tenant string, alerts []backend.Alert) map[string]struct{} {
	out := make(map[string]struct{}, len(alerts))
	for _, a := range alerts {
		out[groupKeyOf(tenant, a.Labels[labelAlertname])] = struct{}{}
	}
	return out
}

func countMissing(keys, from map[string]struct{}) int {
	n := 0
	for k := range keys {
		if _, ok := from[k]; !ok {
			n++
		}
	}
	return n
}

// pollDeltaText drops a zero term so an all-new poll reads
// "+3 new" rather than "+3 new, -0 resolved".
func pollDeltaText(newCount, resolvedCount int) string {
	parts := make([]string, 0, 2)
	if newCount > 0 {
		parts = append(parts, fmt.Sprintf("+%d new", newCount))
	}
	if resolvedCount > 0 {
		parts = append(parts, fmt.Sprintf("-%d resolved", resolvedCount))
	}
	return strings.Join(parts, ", ")
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
	survivors := p.applyFilter(flat)
	p.groups = aggregate(survivors, p.labelCols)
	p.labelWidths = p.measureLabelColumns()
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
				if groupKeyOf(tenant, a.Labels[labelAlertname]) == p.focusGroupKey {
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
func aggregate(in []alertEntry, cols []labelcol.Column) []alertGroup {
	byKey := map[string]*alertGroup{}
	order := make([]string, 0)
	for _, e := range in {
		name := e.a.Labels[labelAlertname]
		key := groupKeyOf(e.tenant, name)
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
		g.labelCells = rollupCells(g.instances, cols)
		out = append(out, *g)
	}
	return out
}

// rollupCells returns nil when no columns are configured, so a page
// without them allocates nothing per group.
func rollupCells(instances []backend.Alert, cols []labelcol.Column) []string {
	if len(cols) == 0 {
		return nil
	}
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, labelcol.AggregateCell(instances, c.Label))
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

func (p *Page) applyFilter(in []alertEntry) []alertEntry {
	if p.FilterMatchAll() && p.stateFilter == "" {
		// `in` is recompute's local `flat` slice, consumed only by
		// aggregate() which reads it without retaining it. Returning it
		// unchanged avoids an O(N) copy that would fire every poll tick.
		return in
	}
	kept := in
	if p.stateFilter != "" {
		kept = make([]alertEntry, 0, len(in))
		for _, e := range in {
			if string(e.a.State) == p.stateFilter {
				kept = append(kept, e)
			}
		}
	}
	if p.FilterMatchAll() {
		// kept was allocated just above, so the caller holds no
		// reference to it and a second copy would buy nothing.
		return kept
	}
	// Only an expression can name COUNT or AGE, and the pre-pass costs
	// a key and a map slot per instance, so a text or selector buffer
	// skips it.
	var stats map[string]groupStat
	if p.FilterIsExpr() {
		stats = groupStats(kept)
	}
	now := p.now()
	out := make([]alertEntry, 0, len(kept))
	for _, e := range kept {
		row := filterexpr.Row{
			Now:      now,
			Labels:   e.a.Labels,
			Text:     e.lowerComposite,
			State:    string(e.a.State),
			Instance: filterexpr.Present,
		}
		if stats != nil {
			g := stats[groupKey(e)]
			row.Count, row.CountAvail = g.count, filterexpr.Present
			row.Start, row.AgeAvail = g.oldestStart, filterexpr.Present
		}
		if p.FilterMatch(row) {
			out = append(out, e)
		}
	}
	return out
}

// groupStat carries the two group-level values an expression can
// compare against.
type groupStat struct {
	count       int
	oldestStart time.Time
}

// groupStats accumulates count and oldest start exactly as aggregate
// does: the filter compares the same AGE the column renders, so the
// two rules have to move together.
func groupStats(in []alertEntry) map[string]groupStat {
	out := map[string]groupStat{}
	for _, e := range in {
		key := groupKey(e)
		g, ok := out[key]
		if !ok || e.a.StartsAt.Before(g.oldestStart) {
			g.oldestStart = e.a.StartsAt
		}
		g.count++
		out[key] = g
	}
	return out
}

func groupKey(e alertEntry) string {
	return groupKeyOf(e.tenant, e.a.Labels[labelAlertname])
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

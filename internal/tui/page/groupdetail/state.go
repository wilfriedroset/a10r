// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"sort"
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// recompute rebuilds common, the entry slice, and the sorted/filtered
// view from p.instances, then re-anchors the cursor on the focused
// fingerprint. Called on every data / filter / sort change.
func (p *Page) recompute() {
	p.common = backend.CommonLabels(p.instances)
	flat := p.buildEntries()
	p.view = filterEntries(flat, p.Filter, p.stateFilter)
	p.sorter.Apply(p.view)
	p.resolveFocus()
	p.Clamp(len(p.view))
	p.snapshotFocus()
}

// buildEntries projects every instance into an instanceEntry,
// precomputing the filter blob and the distinguishing-label summary
// against the stable common set.
func (p *Page) buildEntries() []instanceEntry {
	out := make([]instanceEntry, 0, len(p.instances))
	for _, a := range p.instances {
		out = append(out, instanceEntry{
			a:                  a,
			lowerComposite:     lowerComposite(a),
			distinguishSummary: distinguishingSummary(a, p.common),
		})
	}
	return out
}

// resolveFocus anchors the cursor on the focused fingerprint when the
// instance is still in view. When the focus is gone from p.instances
// entirely (instance resolved), clears it so a later widening does
// not chase a phantom; when only the filter hid it, keeps it so a
// filter-clear re-anchors.
func (p *Page) resolveFocus() {
	if p.focusFingerprint == "" {
		return
	}
	for i, e := range p.view {
		if e.a.Fingerprint == p.focusFingerprint {
			p.SetIndex(i, len(p.view))
			return
		}
	}
	if !p.instancesKnow(p.focusFingerprint) {
		p.focusFingerprint = ""
	}
}

// instancesKnow reports whether any current instance carries fp — used
// to decide whether a focus miss is "filtered out" (keep) or "gone"
// (clear).
func (p *Page) instancesKnow(fp string) bool {
	for _, a := range p.instances {
		if a.Fingerprint == fp {
			return true
		}
	}
	return false
}

// snapshotFocus captures the fingerprint under the cursor for the
// next recompute. Empty view preserves the prior fingerprint so a
// filter-clear re-anchors rather than landing on row 0.
func (p *Page) snapshotFocus() {
	if p.Index() < len(p.view) {
		p.focusFingerprint = p.view[p.Index()].a.Fingerprint
	}
}

// matchingInstances returns the subset of alerts whose alertname
// equals the page's. Called inside the DataMsg store callback so the
// page only ever holds the instances for its own (tenant, alertname).
func (p *Page) matchingInstances(alerts []backend.Alert) []backend.Alert {
	out := make([]backend.Alert, 0, len(alerts))
	for _, a := range alerts {
		if a.Labels["alertname"] == p.alertName {
			out = append(out, a)
		}
	}
	return out
}

// cycleStateFilter walks "" → active → suppressed → unprocessed → ""
// per the state-filter binding's intent.
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

// filterEntries returns only the entries matching both the search and
// state filters. When the search buffer is a Prometheus label matcher
// (`cluster_id=99`, `cluster_id=~9.*`, …) it filters by that label
// predicate; otherwise it runs through footer.NewMatcher (substring /
// fuzzy / literal / regex over the values). Shares the input backing
// when nothing filters (recompute owns the slice) to avoid an O(N)
// copy every poll tick.
func filterEntries(in []instanceEntry, search, state string) []instanceEntry {
	if pred, ok := matcher.LabelPredicate(search); ok {
		return filterByLabel(in, pred, state)
	}
	m := footer.NewMatcher(search)
	if m.MatchAll() && state == "" {
		return in
	}
	out := make([]instanceEntry, 0, len(in))
	for _, e := range in {
		if state != "" && string(e.a.State) != state {
			continue
		}
		if !m.MatchAll() && !m.Match(e.lowerComposite) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// filterByLabel keeps entries whose instance labels satisfy the label
// predicate (and the state filter). Separate from filterEntries' text
// path so each stays a flat loop rather than a branch-in-loop.
func filterByLabel(in []instanceEntry, pred func(map[string]string) bool, state string) []instanceEntry {
	out := make([]instanceEntry, 0, len(in))
	for _, e := range in {
		if state != "" && string(e.a.State) != state {
			continue
		}
		if !pred(e.a.Labels) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// distinguishingSummary renders a's distinguishing labels (those
// outside common) as `k=v · k=v`. The `instance` label is pinned
// first so truncation never eats the primary identifier; the rest
// are sorted for a stable, scannable order.
func distinguishingSummary(a backend.Alert, common map[string]string) string {
	dist := backend.DistinguishingLabels(a, common)
	if len(dist) == 0 {
		return ""
	}
	keys := make([]string, 0, len(dist))
	for k := range dist {
		// `instance` is pinned first below; `severity` is excluded
		// entirely because the group-detail page carries it in a
		// dedicated SEVERITY column — repeating it here is noise.
		if k == sortKeyInstance || k == "severity" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if _, ok := dist[sortKeyInstance]; ok {
		keys = append([]string{sortKeyInstance}, keys...)
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+dist[k])
	}
	return strings.Join(parts, " · ")
}

// lowerComposite concatenates the lower-cased label and annotation
// values into a NUL-separated blob the substring filter reads. Built
// once per recompute.
func lowerComposite(a backend.Alert) string {
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
	write := func(v string) {
		if !first {
			b.WriteByte(0)
		}
		first = false
		b.WriteString(strings.ToLower(v))
	}
	for _, v := range a.Labels {
		write(v)
	}
	for _, v := range a.Annotations {
		write(v)
	}
	return b.String()
}

// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// totalSilences is the unfiltered silence count within the active
// scope, capped to restrictIDs when set — same role as the alerts
// page's totalAlerts.
func (p *Page) totalSilences() int {
	n := 0
	for tenant, sils := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		if len(p.restrictIDs) == 0 {
			n += len(sils)
			continue
		}
		for _, s := range sils {
			if _, ok := p.restrictIDs[s.ID]; ok {
				n++
			}
		}
	}
	return n
}

// recompute rebuilds p.view by walking byTenant, applying the
// restrictIDs gate (when set), scope, and substring filters, then
// sorting. Cursor is preserved across rebuilds by silence ID when
// possible — see snapshotFocus.
func (p *Page) recompute() {
	p.view = filterSilences(p.scopedEntries(), p.Filter)
	p.sorter.Apply(p.view)
	if p.focusID != "" {
		for i, e := range p.view {
			if e.s.ID == p.focusID {
				p.SetIndex(i, len(p.view))
				return
			}
		}
	}
	p.Clamp(len(p.view))
	p.snapshotFocus()
}

// scopedEntries flattens byTenant into filterable entries, honouring
// the scope gate and the restrictIDs allowlist (when set). The
// composite cache is built once per entry here, not per keystroke.
func (p *Page) scopedEntries() []silenceEntry {
	total := 0
	for tenant, sils := range p.byTenant {
		if p.ScopeIncludes(tenant) {
			total += len(sils)
		}
	}
	flat := make([]silenceEntry, 0, total)
	for tenant, sils := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		for _, s := range sils {
			if len(p.restrictIDs) > 0 {
				if _, ok := p.restrictIDs[s.ID]; !ok {
					continue
				}
			}
			flat = append(flat, silenceEntry{
				s:              s,
				tenant:         tenant,
				lowerComposite: silenceLowerComposite(s),
			})
		}
	}
	return flat
}

// filterSilences returns a fresh slice with the entries whose
// lower-cased composite (built at recompute) matches the query —
// matching mode (substring / fuzzy / literal / regex) is auto-
// detected by footer.NewMatcher per the keybindings.md contract.
// Empty query short-circuits to the input. The case-fold work
// runs once per ingest (composite cache) and once per recompute
// (matcher needle), not once per keystroke per entry.
func filterSilences(in []silenceEntry, query string) []silenceEntry {
	matcher := footer.NewMatcher(query)
	if matcher.MatchAll() {
		// Clone to keep the filter output independent of the caller's
		// input slice — downstream mutations on the view (cursor
		// re-anchoring, mark management) must not bleed into the
		// per-tenant source aggregation.
		out := make([]silenceEntry, len(in))
		copy(out, in)
		return out
	}
	out := make([]silenceEntry, 0, len(in))
	for _, e := range in {
		if matcher.Match(e.lowerComposite) {
			out = append(out, e)
		}
	}
	return out
}

// silenceLowerComposite concatenates every field the filter prompt
// matches on (ID, CreatedBy, Comment, State, matcher Names and
// Values) into a single lower-cased string. NUL-separated so a
// query can't accidentally span field boundaries.
func silenceLowerComposite(s backend.Silence) string {
	var b strings.Builder
	// Approximation: every field plus separators. Over-allocates
	// slightly when matchers are short; cheap relative to the
	// allocation churn the cache replaces.
	estimate := len(s.ID) + len(s.CreatedBy) + len(s.Comment) + len(s.State) + len(s.Matchers)*16
	b.Grow(estimate)
	b.WriteString(strings.ToLower(s.ID))
	b.WriteByte(0)
	b.WriteString(strings.ToLower(s.CreatedBy))
	b.WriteByte(0)
	b.WriteString(strings.ToLower(s.Comment))
	b.WriteByte(0)
	b.WriteString(strings.ToLower(string(s.State)))
	for _, m := range s.Matchers {
		b.WriteByte(0)
		b.WriteString(strings.ToLower(m.Name))
		b.WriteByte(0)
		b.WriteString(strings.ToLower(m.Value))
	}
	return b.String()
}

// snapshotFocus captures the silence ID under the cursor so the
// next recompute can re-find the same row by ID after sorts /
// filters / poll updates churn the index space. Empty when no
// row is focused; recompute clears p.focusID by passing through
// here.
func (p *Page) snapshotFocus() {
	if p.Index() < len(p.view) {
		p.focusID = p.view[p.Index()].s.ID
		return
	}
	p.focusID = ""
}

// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
)

// totalSilences is the unfiltered silence count within the active
// scope — same role as the alerts page's totalAlerts.
func (p *Page) totalSilences() int {
	n := 0
	for tenant, sils := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		n += len(sils)
	}
	return n
}

// scopeIncludes reports whether tenant should appear in the view.
// Empty / "all" includes everyone; otherwise the scope is matched
// against the comma-joined list (so a Ctrl+T multi-select like
// "prod,staging" lights up both backends).
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

// showTenantColumn reports whether the view spans more than one
// in-scope tenant — TENANT column is rendered iff so.
func (p *Page) showTenantColumn() bool {
	if p.scope != scopeAll {
		return false
	}
	in := 0
	for tenant := range p.byTenant {
		if p.scopeIncludes(tenant) {
			in++
		}
	}
	return in > 1
}

// recompute rebuilds p.view by walking byTenant, applying the
// scope and substring filters, then sorting. Cursor is preserved
// across rebuilds by silence ID when possible — see snapshotFocus.
func (p *Page) recompute() {
	defer p.recomputeScroll()
	total := 0
	for tenant, sils := range p.byTenant {
		if p.scopeIncludes(tenant) {
			total += len(sils)
		}
	}
	flat := make([]silenceEntry, 0, total)
	for tenant, sils := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, s := range sils {
			flat = append(flat, silenceEntry{
				s:              s,
				tenant:         tenant,
				lowerComposite: silenceLowerComposite(s),
			})
		}
	}
	p.view = filterSilences(flat, p.filter)
	p.sorter.Apply(p.view)
	if p.focusID != "" {
		for i, e := range p.view {
			if e.s.ID == p.focusID {
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
// body height. Mirror of the alerts page's helper — see that file
// for the rationale on keeping View as a pure reader.
func (p *Page) recomputeScroll() {
	if p.bodyHeight <= 0 {
		return
	}
	p.topRow = cursor.ReconcileScroll(p.cursor, p.topRow, p.bodyHeight, len(p.view))
}

// filterSilences returns a fresh slice with the entries whose
// lower-cased composite (built at recompute) contains the
// lowercased query as a substring. Empty filter returns the input
// unchanged. The case-fold work runs once per ingest, not once per
// keystroke per entry.
func filterSilences(in []silenceEntry, query string) []silenceEntry {
	if query == "" {
		return in
	}
	q := strings.ToLower(query)
	out := make([]silenceEntry, 0, len(in))
	for _, e := range in {
		if strings.Contains(e.lowerComposite, q) {
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
	if p.cursor < len(p.view) {
		p.focusID = p.view[p.cursor].s.ID
		return
	}
	p.focusID = ""
}

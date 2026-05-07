// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
)

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
	flat := make([]silenceEntry, 0)
	for tenant, sils := range p.byTenant {
		if !p.scopeIncludes(tenant) {
			continue
		}
		for _, s := range sils {
			flat = append(flat, silenceEntry{s: s, tenant: tenant})
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

// filterSilences returns a fresh slice with the entries whose
// rendered text contains the lowercased query as a substring.
// Empty filter returns the input unchanged.
func filterSilences(in []silenceEntry, query string) []silenceEntry {
	if query == "" {
		return in
	}
	q := strings.ToLower(query)
	out := make([]silenceEntry, 0, len(in))
	for _, e := range in {
		if silenceMatches(e.s, q) {
			out = append(out, e)
		}
	}
	return out
}

// silenceMatches walks the user-visible text fields. The query
// caller must already be lowercased. ID is included so a UUID
// prefix typed into the filter prompt finds the row whose UUID
// column is clipped to 8 chars.
func silenceMatches(s backend.Silence, q string) bool {
	if strings.Contains(strings.ToLower(s.ID), q) ||
		strings.Contains(strings.ToLower(s.CreatedBy), q) ||
		strings.Contains(strings.ToLower(s.Comment), q) ||
		strings.Contains(strings.ToLower(string(s.State)), q) {
		return true
	}
	for _, m := range s.Matchers {
		if strings.Contains(strings.ToLower(m.Name), q) ||
			strings.Contains(strings.ToLower(m.Value), q) {
			return true
		}
	}
	return false
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

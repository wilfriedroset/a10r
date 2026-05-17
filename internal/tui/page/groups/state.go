// SPDX-License-Identifier: Apache-2.0

package groups

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// totalGroups is the in-scope count regardless of filter.
func (p *Page) totalGroups() int {
	n := 0
	for tenant, gs := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		n += len(gs)
	}
	return n
}

// recompute rebuilds p.flat from byTenant + scope, preserving any
// in-place expanded flags by group identity (label-set + tenant)
// across refresh ticks. New groups land collapsed; vanished
// groups simply drop out. Sort is applied after the slice is
// rebuilt so the active sort column + direction govern row order.
func (p *Page) recompute() {
	defer p.ReconcileScroll(len(p.rows()))
	prev := make(map[string]bool, len(p.flat))
	for i, e := range p.flat {
		prev[groupKey(e)] = i < len(p.expanded) && p.expanded[i]
	}
	p.flat = p.flat[:0]
	for tenant, gs := range p.byTenant {
		if !p.ScopeIncludes(tenant) {
			continue
		}
		for _, g := range gs {
			p.flat = append(p.flat, groupEntry{
				g:            g,
				tenant:       tenant,
				severityRank: groupSeverityRank(g),
				common:       commonLabels(g.Alerts),
				lowerSummary: strings.ToLower(labelSummary(g.Labels)),
			})
		}
	}
	p.cachedRows = nil
	p.sorter.Apply(p.flat)
	p.expanded = make([]bool, len(p.flat))
	for i, e := range p.flat {
		if prev[groupKey(e)] {
			p.expanded[i] = true
		}
	}
	// Resolve cursor by focusKey when we have one to follow so the
	// user stays on the same logical row across re-sort / scope /
	// poll. Falls through to the clamp + re-snapshot path when
	// focus is empty or the focused row vanished.
	if p.focusKey != "" {
		rows := p.rows()
		for i, r := range rows {
			if rowKey(p.flat, r) == p.focusKey {
				p.Cursor = i
				return
			}
		}
	}
	p.ClampCursor(len(p.rows()))
	p.ReconcileScroll(len(p.rows()))
	p.snapshotFocus()
}

// rowKey builds the focus identity for r. Group headers use the
// stable groupKey; leaf rows append the alertname so the cursor
// can ride a specific alert across refreshes even when its
// position inside the group changes.
func rowKey(flat []groupEntry, r row) string {
	if r.groupIdx < 0 || r.groupIdx >= len(flat) {
		return ""
	}
	g := flat[r.groupIdx]
	if r.alertIdx < 0 {
		return groupKey(g)
	}
	if r.alertIdx >= len(g.g.Alerts) {
		return groupKey(g)
	}
	return groupKey(g) + "\x00" + g.g.Alerts[r.alertIdx].Labels["alertname"]
}

// snapshotFocus captures the rowKey of the row currently under
// the cursor so the next recompute can re-resolve it. Empty
// view leaves focus empty.
func (p *Page) snapshotFocus() {
	rows := p.rows()
	if p.Cursor < 0 || p.Cursor >= len(rows) {
		p.focusKey = ""
		return
	}
	p.focusKey = rowKey(p.flat, rows[p.Cursor])
}

// groupSeverityRank returns the worst severity in g, encoded so
// higher = more severe. Reuses backend.SeverityRank so the
// alerts page and the groups page agree on what "worst" means.
func groupSeverityRank(g backend.AlertGroup) int {
	worst := 0
	for _, a := range g.Alerts {
		if r := backend.SeverityRank(a.Labels); r > worst {
			worst = r
		}
	}
	return worst
}

// groupKey uniquely identifies a group across refreshes: tenant +
// sorted label-set. Used to preserve expanded state when the
// underlying slice ordering changes between polls.
func groupKey(e groupEntry) string {
	return e.tenant + "\x00" + labelSummary(e.g.Labels)
}

// row is one rendered line. groupIdx points at the parent group;
// alertIdx is -1 for a group header, ≥0 for a leaf.
type row struct {
	groupIdx int
	alertIdx int
}

// rows builds the visible row list from p.flat + p.expanded,
// skipping any group whose label-set doesn't match p.Filter (when
// set). Leaves of an expanded matched group always appear — once
// the user expands a matched group, every alert in it shows up
// regardless of whether the alert's labels would match the filter
// in isolation.
//
// Cached: callers (View, navigation handlers, snapshotFocus) hit
// rows() many times per frame. State mutations that change the
// rendered set invalidate p.cachedRows; this method rebuilds and
// re-caches on the next call. Filter is matched against the
// per-entry lowerSummary populated at recompute, so the inner loop
// avoids a fresh strings.ToLower(labelSummary(...)) per call.
func (p *Page) rows() []row {
	if p.cachedRows != nil {
		return p.cachedRows
	}
	matcher := footer.NewMatcher(p.Filter)
	out := make([]row, 0, len(p.flat))
	for gi, e := range p.flat {
		if !matcher.MatchAll() && !matcher.Match(e.lowerSummary) {
			continue
		}
		out = append(out, row{groupIdx: gi, alertIdx: -1})
		if gi < len(p.expanded) && p.expanded[gi] {
			for ai := range e.g.Alerts {
				out = append(out, row{groupIdx: gi, alertIdx: ai})
			}
		}
	}
	p.cachedRows = out
	return out
}

// visibleGroups returns the slice of in-scope groups whose
// label-set matches p.Filter — same predicate rows() uses for
// headers. Reads the per-entry lowerSummary cache populated at
// recompute to avoid re-running strings.ToLower(labelSummary())
// on every call.
func (p *Page) visibleGroups() []backend.AlertGroup {
	matcher := footer.NewMatcher(p.Filter)
	if matcher.MatchAll() {
		out := make([]backend.AlertGroup, len(p.flat))
		for i, e := range p.flat {
			out[i] = e.g
		}
		return out
	}
	out := make([]backend.AlertGroup, 0, len(p.flat))
	for _, e := range p.flat {
		if matcher.Match(e.lowerSummary) {
			out = append(out, e.g)
		}
	}
	return out
}

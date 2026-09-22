// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// benchAlerts builds n synthetic alerts spread across `tenants` tenants
// with a mix of severities so the comparator and severity-coloured
// render path actually do work — uniform input understates the win.
func benchAlerts(n, tenants int) map[string][]backend.Alert {
	out := map[string][]backend.Alert{}
	severities := []string{"critical", "warning", "info"}
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	for i := range n {
		tenant := "t" + strconv.Itoa(i%tenants)
		out[tenant] = append(out[tenant], backend.Alert{
			Fingerprint: fmt.Sprintf("fp-%06d", i),
			Labels: map[string]string{
				"alertname": fmt.Sprintf("Alert%04d", i),
				"severity":  severities[i%len(severities)],
				"instance":  fmt.Sprintf("host-%03d.example.com", i),
				"team":      "platform",
			},
			Annotations: map[string]string{
				"summary":     fmt.Sprintf("alert %d firing", i),
				"description": fmt.Sprintf("description for alert %d on host-%03d", i, i),
			},
			State:    backend.AlertStateActive,
			StartsAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	return out
}

// BenchmarkAlertsRecompute_1000 measures the full recompute pipeline
// (flat assembly + filter + sort) on a 1k-alert × 4-tenant set.
func BenchmarkAlertsRecompute_1000(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(1000, 4)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recompute()
	}
}

// BenchmarkAlertsRecompute_5000 mirrors the above at storm-time
// scale: 5k alerts × 10 tenants.
func BenchmarkAlertsRecompute_5000(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(5000, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recompute()
	}
}

// BenchmarkAlertsFilterTyping mimics the per-keystroke recompute the
// page does while the user types into the `/` prompt. The per-entry
// case-folded cache on each alertEntry is the load-bearing
// optimisation; any regression there surfaces here first.
func BenchmarkAlertsFilterTyping(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(2000, 10)
	queries := []string{"a", "al", "ale", "alert", "alert4", "alert42"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		p.SetFilter(queries[i%len(queries)])
		p.recompute()
	}
}

// BenchmarkAlertsRenderRows_1000 measures one frame of the row
// renderer at 1k alerts. Only the top maxRows fit on screen, so the
// actual work is bounded by terminal height — but the per-row hot
// path runs at full width and any per-row allocation regression
// surfaces here first.
func BenchmarkAlertsRenderRows_1000(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(1000, 4)
	p.recompute()
	p.SetViewport(40, len(p.groups))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = p.renderRows(160, 40)
	}
}

// BenchmarkAlertsDataMsgIngest measures the poll.DataMsg arrival
// path: the App stashes the payload, the page absorbs it, recompute
// runs, the user sees fresh rows. Approximates the 15 s × 10 tenants
// = 40 ingests/min steady-state pressure on a busy fleet.
func BenchmarkAlertsDataMsgIngest(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now})
	payload := benchAlerts(500, 1)["t0"]

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = p.Update(poll.DataMsg{
			Resource: payload,
			Tenant:   "t0",
		})
	}
}

// BenchmarkAlertsRenderRowsFiltered measures one frame with a filter
// active, where every visible cell is scanned for the characters to
// paint. Read it against BenchmarkAlertsRenderRows: the paint is a
// per-frame cost on the visible window only, and the budget for it is
// 1.5x the unfiltered frame. Every filter here keeps all 1000 groups
// on purpose — a selective one would shrink the view, and the cheaper
// columnWidths pass would pay for the paint.
func BenchmarkAlertsRenderRowsFiltered(b *testing.B) {
	styles := testutil.LoadStyles(b)
	for _, tc := range []struct{ name, filter string }{
		{"substring", "alert"},
		{"fuzzy", "~alt"},
		{"regex", "a.*t"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			p := New(Options{Styles: styles, Now: time.Now})
			p.byTenant = benchAlerts(1000, 4)
			p.SetFilter(tc.filter)
			p.recompute()
			p.SetViewport(40, len(p.groups))

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = p.renderRows(160, 40)
			}
		})
	}
}

// BenchmarkAlertsRecomputeFiltered10k puts the two filter paths side
// by side at storm scale: the substring baseline against a three-term
// expression, which pays for a per-group pre-aggregation pass on top
// of the per-instance evaluation. The expression budget is 2x the
// baseline. Both filters keep every instance on purpose -- a
// selective one would skip the aggregate and sort downstream and
// flatter the comparison; the leading false disjunct stops the OR
// short-circuiting away the other two terms.
func BenchmarkAlertsRecomputeExpr10k(b *testing.B) {
	styles := testutil.LoadStyles(b)
	for _, tc := range []struct{ name, filter string }{
		{"substring", "alert"},
		{"expr", "severity=nope || count>=1 && age>1m"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			p := New(Options{Styles: styles, Now: time.Now})
			p.byTenant = benchAlerts(10000, 10)
			p.SetFilter(tc.filter)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p.recompute()
			}
		})
	}
}

// benchColumns is a five-column configuration over a mix of labels
// the synthetic set has and labels it does not, so the rollup pays
// for both the agreeing-value path and the missing-label path.
func benchColumns() []config.Column {
	return []config.Column{
		{Label: "severity", Title: "SEV"},
		{Label: "instance"},
		{Label: "team"},
		{Label: "cluster"},
		{Label: "region"},
	}
}

// BenchmarkAlertsRecomputeLabelColumns measures what five
// user-declared columns add to recompute. Read it against
// BenchmarkAlertsRecompute1k: aggregate now walks every instance once
// per column, and that gap is the whole ingest-side price of the
// feature.
func BenchmarkAlertsRecomputeLabelColumns(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now, Columns: benchColumns()})
	p.byTenant = benchAlerts(1000, 4)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recompute()
	}
}

// BenchmarkAlertsRenderRowsLabelColumns measures one frame with five
// user columns. Read it against BenchmarkAlertsRenderRows: the widths
// are measured in recompute, so the gap here is per-row cell work
// only. A regression that moves the measuring scan back into the
// frame shows up as a multiple, not a margin.
func BenchmarkAlertsRenderRowsLabelColumns(b *testing.B) {
	styles := testutil.LoadStyles(b)
	p := New(Options{Styles: styles, Now: time.Now, Columns: benchColumns()})
	p.byTenant = benchAlerts(1000, 4)
	p.recompute()
	p.SetViewport(40, len(p.groups))

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = p.renderRows(160, 40)
	}
}

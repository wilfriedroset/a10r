// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
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
// (flat assembly + filter + sort) on a 1k-alert × 4-tenant set. F3
// + F6 + F7 wins land here.
func BenchmarkAlertsRecompute_1000(b *testing.B) {
	styles := testutil.LoadStylesB(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(1000, 4)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recompute()
	}
}

// BenchmarkAlertsRecompute_5000 mirrors the above at storm-time
// scale: 5k alerts × 10 tenants. The audit assumed 1k; this bench
// reflects the user's actual ceiling.
func BenchmarkAlertsRecompute_5000(b *testing.B) {
	styles := testutil.LoadStylesB(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(5000, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recompute()
	}
}

// BenchmarkAlertsFilterTyping mimics the per-keystroke recompute the
// page does while the user types into the `/` prompt. F14's
// per-entry case-folded cache is the load-bearing optimisation here.
func BenchmarkAlertsFilterTyping(b *testing.B) {
	styles := testutil.LoadStylesB(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(2000, 10)
	queries := []string{"a", "al", "ale", "alert", "alert4", "alert42"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		p.Filter = queries[i%len(queries)]
		p.recompute()
	}
}

// BenchmarkAlertsRenderRows_1000 measures one frame of the row
// renderer at 1k alerts. Only the top maxRows fit on screen, so the
// actual work is bounded by terminal height — but the per-row hot
// path runs at full width and any per-row allocation regression
// surfaces here first.
func BenchmarkAlertsRenderRows_1000(b *testing.B) {
	styles := testutil.LoadStylesB(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchAlerts(1000, 4)
	p.recompute()
	p.SetViewport(40, len(p.view))

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
	styles := testutil.LoadStylesB(b)
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

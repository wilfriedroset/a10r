// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func benchSilences(n, tenants int) map[string][]backend.Silence {
	out := map[string][]backend.Silence{}
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	for i := range n {
		tenant := "t" + strconv.Itoa(i%tenants)
		out[tenant] = append(out[tenant], backend.Silence{
			ID:        fmt.Sprintf("sil-%06d", i),
			CreatedBy: fmt.Sprintf("user%d@example.com", i%50),
			Comment:   fmt.Sprintf("triage silence %d for ongoing maintenance window", i),
			StartsAt:  now.Add(-time.Duration(i) * time.Minute),
			EndsAt:    now.Add(time.Duration(i) * time.Minute),
			State:     backend.SilenceStateActive,
			Matchers: []backend.Matcher{
				{Name: "alertname", Value: fmt.Sprintf("Alert%04d", i), IsEqual: true},
				{Name: "severity", Value: "critical", IsEqual: true},
				{Name: "team", Value: "platform", IsEqual: true},
			},
		})
	}
	return out
}

// BenchmarkSilenceMatches_500 measures the per-keystroke filter loop
// at 500 silences. F14's per-entry case-folded composite is the
// load-bearing optimisation here; without it the loop runs
// strings.ToLower on every searchable field per row per keystroke.
func BenchmarkSilenceMatches_500(b *testing.B) {
	styles := testutil.LoadStylesB(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchSilences(500, 4)
	p.recompute()
	queries := []string{"a", "al", "ale", "alert", "alert4", "alert42"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		p.filter = queries[i%len(queries)]
		p.recompute()
	}
}

// BenchmarkSilencesRecompute_2000 measures the full recompute pipeline
// at storm-scale silence counts (5 long-running silences per alert
// across 10 tenants).
func BenchmarkSilencesRecompute_2000(b *testing.B) {
	styles := testutil.LoadStylesB(b)
	p := New(Options{Styles: styles, Now: time.Now})
	p.byTenant = benchSilences(2000, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recompute()
	}
}

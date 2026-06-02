// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"runtime"
	"testing"
	"time"

	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// TestSoak_AlertsHeapStable drives the alerts page through many
// DataMsg ingest + filter cycles and asserts the heap doesn't drift
// upward — captures the multi-day-session regression risk.
//
// Skipped under -short. Steady-state allocation churn is fine; what
// matters is that goroutine count and live heap settle at a bound.
func TestSoak_AlertsHeapStable(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test: skipped under -short")
	}

	styles := testutil.LoadStyles(t)
	p := New(Options{Styles: styles, Now: time.Now})

	const cycles = 5_000
	const alertsPerCycle = 500
	queries := []string{"", "alert", "host", "critical", "team", ""}

	// Warm-up: prime byTenant + run a few cycles so heap reaches
	// steady state before we sample it.
	payload := benchAlerts(alertsPerCycle, 1)["t0"]
	for range 100 {
		_, _ = p.Update(poll.DataMsg{Resource: payload, Tenant: "t0"})
	}

	runtime.GC()
	runtime.GC()
	var beforeStats runtime.MemStats
	runtime.ReadMemStats(&beforeStats)
	beforeGoroutines := runtime.NumGoroutine()

	for i := range cycles {
		_, _ = p.Update(poll.DataMsg{Resource: payload, Tenant: "t0"})
		p.Filter = queries[i%len(queries)]
		p.recompute()
	}

	runtime.GC()
	runtime.GC()
	var afterStats runtime.MemStats
	runtime.ReadMemStats(&afterStats)
	afterGoroutines := runtime.NumGoroutine()

	heapGrowth := int64(afterStats.HeapAlloc) - int64(beforeStats.HeapAlloc)
	t.Logf("soak: cycles=%d heap before=%d after=%d Δ=%d goroutines before=%d after=%d",
		cycles, beforeStats.HeapAlloc, afterStats.HeapAlloc, heapGrowth,
		beforeGoroutines, afterGoroutines)

	// Tolerance bands: live heap shouldn't grow more than 4 MiB
	// between the two samples (steady state is bounded by the
	// pollCache + filtered view; a leak compounds linearly with
	// cycles). Goroutine count must not drift at all — every
	// goroutine the page spawns must be tied to its lifecycle.
	const heapToleranceBytes = 4 << 20
	if heapGrowth > heapToleranceBytes {
		t.Fatalf("soak: heap grew by %d bytes (> %d tolerance); possible leak",
			heapGrowth, heapToleranceBytes)
	}
	if afterGoroutines > beforeGoroutines {
		t.Fatalf("soak: goroutine count grew %d → %d; possible leak",
			beforeGoroutines, afterGoroutines)
	}
}

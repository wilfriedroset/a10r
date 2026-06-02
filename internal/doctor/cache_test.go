// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
)

// fakeClock is the test-side clock. now is the current returned
// instant; advance() bumps it forward. Concurrent tests that share
// a fakeClock must guard advance/now externally, but the suite
// below uses one fakeClock per test so no internal lock is needed.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) advance(d time.Duration) { f.now = f.now.Add(d) }

// countingChecker records how many times Run is invoked. The
// returned Result carries a sequence number in the message so
// tests can assert "the cache returned the *first* run's result"
// vs "the cache produced a fresh one".
type countingChecker struct {
	name  string
	calls atomic.Int32
	// nextResult builds the Result for the n-th call (1-indexed).
	// Tests inject this to vary severity / message per call.
	nextResult func(call int32, b config.Backend) Result
}

func (c *countingChecker) Name() string { return c.name }

func (c *countingChecker) Run(_ context.Context, b config.Backend, _ backend.Client) Result {
	n := c.calls.Add(1)
	if c.nextResult != nil {
		return c.nextResult(n, b)
	}
	return Result{Backend: b.Name, Check: c.name, Severity: SeverityOK}
}

func TestWithCache_MissAfterTTL(t *testing.T) {
	t.Parallel()

	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	inner := &countingChecker{
		name: "probe",
		nextResult: func(n int32, b config.Backend) Result {
			return Result{
				Backend:  b.Name,
				Check:    "probe",
				Severity: SeverityOK,
				Message:  "call-" + intToStr(n),
			}
		},
	}
	cached := wrapWithCache([]Checker{inner}, 30*time.Second, clk)[0]

	cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.EqualValues(t, 1, inner.calls.Load())

	// Advance exactly 30s — TTL boundary → cache miss, refresh.
	clk.advance(30 * time.Second)
	second := cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.Equal(t, "call-2", second.Message, "TTL expiry refreshes the entry")
	require.EqualValues(t, 2, inner.calls.Load())

	// Right after refresh, another call within TTL hits cache.
	clk.advance(1 * time.Second)
	third := cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.Equal(t, "call-2", third.Message, "post-refresh entry is itself cached")
	require.EqualValues(t, 2, inner.calls.Load())
}

func TestWithCache_TenantIsolation(t *testing.T) {
	t.Parallel()

	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	inner := &countingChecker{
		name: "probe",
		nextResult: func(n int32, b config.Backend) Result {
			return Result{
				Backend:  b.Name,
				Check:    "probe",
				Severity: SeverityOK,
				Message:  "call-" + intToStr(n),
			}
		},
	}
	cached := wrapWithCache([]Checker{inner}, 30*time.Second, clk)[0]

	prod := cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
	staging := cached.Run(t.Context(), config.Backend{Name: "staging"}, nil)

	// Each tenant has its own slot — both calls hit the inner
	// checker; neither one is served from the other's cache entry.
	require.Equal(t, "prod", prod.Backend)
	require.Equal(t, "call-1", prod.Message)
	require.Equal(t, "staging", staging.Backend)
	require.Equal(t, "call-2", staging.Message)
	require.EqualValues(t, 2, inner.calls.Load())

	// Within TTL, both tenants now serve from cache.
	clk.advance(5 * time.Second)
	require.Equal(t, "call-1", cached.Run(t.Context(), config.Backend{Name: "prod"}, nil).Message)
	require.Equal(t, "call-2", cached.Run(t.Context(), config.Backend{Name: "staging"}, nil).Message)
	require.EqualValues(t, 2, inner.calls.Load())
}

func TestWithCache_CheckNameIsolation(t *testing.T) {
	t.Parallel()

	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	a := &countingChecker{name: "a"}
	b := &countingChecker{name: "b"}

	wrapped := wrapWithCache([]Checker{a, b}, 30*time.Second, clk)
	require.Len(t, wrapped, 2)
	require.Equal(t, "a", wrapped[0].Name())
	require.Equal(t, "b", wrapped[1].Name())

	wrapped[0].Run(t.Context(), config.Backend{Name: "prod"}, nil)
	wrapped[1].Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.EqualValues(t, 1, a.calls.Load())
	require.EqualValues(t, 1, b.calls.Load())

	// Same tenant, second call to each → both served from cache.
	clk.advance(5 * time.Second)
	wrapped[0].Run(t.Context(), config.Backend{Name: "prod"}, nil)
	wrapped[1].Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.EqualValues(t, 1, a.calls.Load(), "checker a's cache entry must not be invalidated by checker b")
	require.EqualValues(t, 1, b.calls.Load())
}

func TestWithCache_ErrorResultsCached(t *testing.T) {
	t.Parallel()

	// The cache-error policy is documented in cache.go: failed
	// Results live in the cache the same way successful ones do,
	// so a known-broken backend gets a 30s breather instead of
	// being hammered. This test pins that contract.
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	inner := &countingChecker{
		name: "probe",
		nextResult: func(n int32, b config.Backend) Result {
			return Result{
				Backend:  b.Name,
				Check:    "probe",
				Severity: SeverityError,
				Message:  "boom-" + intToStr(n),
			}
		},
	}
	cached := wrapWithCache([]Checker{inner}, 30*time.Second, clk)[0]

	first := cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.Equal(t, SeverityError, first.Severity)
	require.Equal(t, "boom-1", first.Message)

	clk.advance(10 * time.Second)
	second := cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
	require.Equal(t, "boom-1", second.Message, "error Results must be cached for the full TTL")
	require.EqualValues(t, 1, inner.calls.Load())
}

func TestWithCache_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	// Hammer one cached checker from many goroutines on the same
	// (tenant, check) key. The cache must not data-race and must
	// not produce more than one underlying Run within the TTL
	// window — every concurrent call after the first is either
	// blocked on the mutex (and then serves from cache) or is the
	// initial probe.
	clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	inner := &countingChecker{name: "probe"}
	cached := wrapWithCache([]Checker{inner}, 30*time.Second, clk)[0]

	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			cached.Run(t.Context(), config.Backend{Name: "prod"}, nil)
		})
	}
	wg.Wait()

	require.EqualValues(t, 1, inner.calls.Load(),
		"concurrent calls must collapse to one underlying Run within TTL")
}

// intToStr renders the call counter as a decimal string. Wraps
// strconv so the test fixtures stay terse.
func intToStr(n int32) string { return strconv.FormatInt(int64(n), 10) }

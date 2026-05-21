// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"sync"
	"time"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/clock"
	"github.com/wilfriedroset/a10r/internal/config"
)

// WithCache wraps each Checker in cs with a TTL-bounded result cache
// keyed by (backend name, check name). Within ttl, repeated Run calls
// return the same Result without re-invoking the underlying check;
// after ttl, the next call refreshes the entry.
//
// The cache is intended for refresh-driven callers (a future TUI
// status page polling every few seconds) where re-issuing the
// network probes on every tick would hammer the backend without
// changing the answer. The one-shot `a10r doctor` CLI does not need
// caching — there's only one call per checker per invocation — but
// wrapping is cheap so the wiring is identical for both.
//
// Cache-error policy: failed Results are cached for the full ttl,
// same as successful ones. Rationale: a 30s window of "we know this
// backend is down, don't hammer it" is the entire point of the
// cache. Operators wanting a fresh probe construct a new decorator
// (or wait out the ttl) — there is no per-call invalidation knob,
// kept deliberately small for the single-window refresh use case.
//
// The returned slice has the same length and order as cs so callers
// can swap WithCache(cs, ...) into any place that previously used
// cs directly.
func WithCache(cs []Checker, ttl time.Duration) []Checker {
	return wrapWithCache(cs, ttl, clock.System{})
}

// wrapWithCache is the DI seam; production reaches it via
// WithCache (pinned to clock.System).
func wrapWithCache(cs []Checker, ttl time.Duration, clk clock.Now) []Checker {
	out := make([]Checker, len(cs))
	for i, c := range cs {
		out[i] = &cachedChecker{
			inner:   c,
			ttl:     ttl,
			clock:   clk,
			entries: map[string]cacheEntry{},
		}
	}
	return out
}

// cacheEntry pairs a stored Result with the time it was recorded.
// Equality of (now - storedAt) >= ttl triggers a refresh.
type cacheEntry struct {
	result   Result
	storedAt time.Time
}

// cachedChecker wraps one Checker. The (tenant, check) key the
// plan calls for collapses to just tenant in this layout: one
// cachedChecker exists per inner Checker, so the check-name half
// of the key is implicit in the wrapper's identity. Different
// checks live in sibling cachedChecker values with their own
// entries maps — isolation by construction, not by string keying.
//
// Concurrent callers serialise on mu; inner.Run executes while
// mu is HELD by design — the typical caller is a single TUI
// goroutine, and the alternative (release-recheck-store) would
// risk duplicate probes during the refresh window for no real
// win at single-digit tenant counts. Different (tenant, check)
// combos still parallelise across sibling cachedCheckers since
// each holds its own mutex; the serialisation is per (tenant,
// check) pair, which is the singleflight contract this cache
// wants. Revisit if a future caller fans out concurrent Run
// calls on the same cachedChecker for many tenants.
//
// entries grows monotonically — no eviction. Acceptable while
// tenant counts stay in single digits per CLAUDE.md; revisit
// when a future deployment fans out across hundreds.
type cachedChecker struct {
	inner   Checker
	ttl     time.Duration
	clock   clock.Now
	mu      sync.Mutex
	entries map[string]cacheEntry
}

func (c *cachedChecker) Name() string { return c.inner.Name() }

// Run returns the cached Result for b.Name when the stored entry
// is younger than ttl; cache misses and TTL expiries invoke
// inner.Run and store the fresh Result.
func (c *cachedChecker) Run(ctx context.Context, b config.Backend, cl backend.Client) Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[b.Name]; ok {
		if c.clock.Now().Sub(e.storedAt) < c.ttl {
			return e.result
		}
	}
	res := c.inner.Run(ctx, b, cl)
	c.entries[b.Name] = cacheEntry{result: res, storedAt: c.clock.Now()}
	return res
}

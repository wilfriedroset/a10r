// SPDX-License-Identifier: Apache-2.0

// Package poll runs one (backend, resource) poll loop with backoff, jitter,
// and asymmetric connection-state emission (per-tick on failure, transition-only
// on success). See ADR-0014. Per containedctx guidance, ctx is a Start argument,
// never stored; only the derived cancel func lives on the struct.
package poll

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/clock"
	"github.com/wilfriedroset/a10r/internal/tui/header"
)

// minPollInterval floors opts.Interval: a safety net so a config-layer regression
// can't drive the loop faster than 100ms × N backends and saturate the terminal
// and the backend unnoticed.
const minPollInterval = 100 * time.Millisecond

// FetchFunc is the per-tick worker; the poller treats the returned payload as
// opaque any and passes its own cancellable ctx through.
type FetchFunc func(ctx context.Context) (any, error)

// SendFunc publishes a tea.Msg into the bubbletea program loop (*tea.Program.Send).
type SendFunc func(tea.Msg)

// DataMsg is emitted on every successful poll tick.
type DataMsg struct {
	// Resource is the opaque payload from FetchFunc; pages type-assert it.
	Resource any
	// Tenant routes the result on multi-tenant pages.
	Tenant string
	// ResourceLabel keys the App-level snapshot cache by (label, tenant).
	ResourceLabel string
	// At is when the fetch completed, for "last refresh Ns ago".
	At time.Time
	// NextAt is when the next tick is scheduled, for the countdown.
	NextAt time.Time
}

// BackendStatusMsg is emitted per failed tick but transition-only on success,
// since DataMsg already drives the success-case UI. See ADR-0014.
type BackendStatusMsg struct {
	Tenant string
	State  header.ConnState
	// Detail is the operator-facing err.Error(), empty on recovery. Already
	// redacted at the transport layer (ADR-0008): never pass a raw bearer token.
	Detail string
	// Failures is the consecutive-failure count; zero on recovery.
	Failures int
	// NextAt is the next attempt time, zero on recovery. Computed once per tick
	// alongside the loop's sleep so "next - now" stays in lockstep with the wait.
	NextAt time.Time
}

// Backoff spaces out attempts after a failure. Fields are public for per-backend
// tuning; the zero value falls back to defaultBackoff.
type Backoff struct {
	// Base is the first delay after a failure.
	Base time.Duration
	// CapMultiplier caps the delay at CapMultiplier × interval.
	CapMultiplier int
	// JitterFraction is the ±fraction added as jitter (0.1 = ±10%); zero disables.
	JitterFraction float64
}

// defaultBackoff is the project default (1s base, 6× cap, ±10% jitter), matching
// the k9s reconnection cadence. A func so callers can't mutate a shared schedule.
func defaultBackoff() Backoff {
	return Backoff{
		Base:           time.Second,
		CapMultiplier:  6,
		JitterFraction: 0.1,
	}
}

// Options bundles construction inputs; a struct so the constructor stays additive.
type Options struct {
	// Tenant tags every emitted message so pages can route by source backend.
	Tenant string
	// Resource labels what the poller fetches; the wiring layer buckets pollers
	// by it so manual `r` refresh targets a specific resource.
	Resource string
	// Interval is the success-case tick spacing (config default 1 minute).
	Interval time.Duration
	// Fetch is the per-tick worker. Must not be nil.
	Fetch FetchFunc
	// Send publishes messages into the program loop. Must not be nil.
	Send SendFunc
	// Clock injects time. nil defaults to clock.System.
	Clock clock.Clock
	// Backoff is the failure-mode delay schedule; zero value uses defaultBackoff.
	Backoff Backoff
}

// Poller is one (backend, resource) loop. Construct via New, drive via Start/Stop.
// Per-tick fields (state, failures) are read and written ONLY from the loop
// goroutine — don't add an external Snapshot() without promoting them under mu.
type Poller struct {
	tenant   string
	resource string
	interval time.Duration
	fetch    FetchFunc
	send     SendFunc
	clock    clock.Clock
	backoff  Backoff

	// state is the last emitted conn state; born ConnUnreachable so the
	// cold-start success tick fires its first recovery emission. Loop-goroutine only.
	state header.ConnState
	// failures counts consecutive errors since last success; drives backoff.
	failures int

	// refresh wakes the loop early. Buffered at 1 so concurrent Refresh calls
	// coalesce into one pending nudge. Drained in the select; never closed.
	refresh chan struct{}

	// mu protects cancel/done, touched only by the public lifecycle methods.
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New constructs a Poller. Required fields (Resource, Interval, Fetch, Send) are
// validated and panic when missing, since that's programmer error. Resource is
// mandatory: a poller missing it would silently bypass the snapshot cache.
func New(opts Options) *Poller {
	if opts.Interval <= 0 {
		panic("poll.New: Interval must be positive")
	}
	if opts.Resource == "" {
		panic("poll.New: Resource must not be empty")
	}
	if opts.Fetch == nil {
		panic("poll.New: Fetch must not be nil")
	}
	if opts.Send == nil {
		panic("poll.New: Send must not be nil")
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.System{}
	}
	bo := opts.Backoff
	if bo.Base == 0 {
		bo = defaultBackoff()
	}
	// Defence in depth: re-floor at minPollInterval so a config-layer regression
	// (e.g. 50ms × 10 backends) can't melt the terminal and the backend unnoticed.
	interval := opts.Interval
	if interval < minPollInterval {
		// Loud-but-not-fatal so an operator sees the clamp rather than wondering
		// why a configured 50ms behaves like 100ms.
		slog.Warn("poll: interval below floor; clamping",
			slog.String("tenant", opts.Tenant),
			slog.String("resource", opts.Resource),
			slog.Duration("requested", interval),
			slog.Duration("floor", minPollInterval),
		)
		interval = minPollInterval
	}
	return &Poller{
		tenant:   opts.Tenant,
		resource: opts.Resource,
		interval: interval,
		fetch:    opts.Fetch,
		send:     opts.Send,
		clock:    clk,
		backoff:  bo,
		refresh:  make(chan struct{}, 1),
		// Cold-start sentinel: Unreachable so a first-tick success trips
		// transitionToConnected and fires the initial recovery emission.
		state: header.ConnUnreachable,
	}
}

// Tenant returns the tenant tag, for the wiring layer's refresh routing.
func (p *Poller) Tenant() string { return p.tenant }

// Resource returns the resource label, for the wiring layer.
func (p *Poller) Resource() string { return p.resource }

// Refresh nudges the loop to fetch immediately, replacing the next scheduled
// wake-up. Non-blocking and coalescing. Failure backoff is left intact.
func (p *Poller) Refresh() {
	select {
	case p.refresh <- struct{}{}:
	default:
	}
}

// Start spawns the polling goroutine, cancellable via Stop or the parent ctx.
// A no-op while a goroutine is alive; after Stop it resets per-tick state so the
// caller sees freshly-constructed behaviour.
func (p *Poller) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	p.cancel = cancel
	p.done = done
	// No data race: the previous goroutine was joined in Stop before reaching here.
	p.state = header.ConnUnreachable
	p.failures = 0
	go func() {
		defer close(done)
		p.run(loopCtx)
	}()
}

// Stop signals the goroutine to exit and joins it, making Stop → Start race-free.
// Idempotent.
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	p.cancel = nil
	p.done = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// run is the polling loop. Exits when ctx is cancelled.
func (p *Poller) run(ctx context.Context) {
	// Tick immediately so the page sees data without waiting a full interval.
	delay, ok := p.tickOnce(ctx)
	if !ok {
		return
	}
	for {
		t := p.clock.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C()
			}
			return
		case <-p.refresh:
			// Manual refresh: tick now without resetting p.failures. Stop the timer
			// so a refresh during backoff doesn't leave one outstanding.
			if !t.Stop() {
				<-t.C()
			}
		case <-t.C():
		}
		delay, ok = p.tickOnce(ctx)
		if !ok {
			return
		}
	}
}

// tickOnce performs one fetch, emits the messages, and returns the next delay.
// The same delay is published as NextAt so the countdown matches the actual
// sleep — computing Backoff.Delay twice would draw different jitter and drift it.
// Returns (_, false) when ctx is done. A refresh nudge landed during the fetch
// is drained here, satisfied by this tick.
func (p *Poller) tickOnce(ctx context.Context) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	res, err := p.fetch(ctx)
	if ctx.Err() != nil {
		// Cancelled mid-fetch: don't transition state — the user stopped us, the
		// backend isn't down.
		return 0, false
	}
	select {
	case <-p.refresh:
	default:
	}
	if err != nil {
		p.failures++
		now := p.clock.Now()
		delay := p.backoff.Delay(p.failures, p.interval)
		next := stateFromErr(err)
		p.state = next
		p.send(BackendStatusMsg{
			Tenant:   p.tenant,
			State:    next,
			Detail:   err.Error(),
			Failures: p.failures,
			NextAt:   now.Add(delay),
		})
		return delay, true
	}
	p.failures = 0
	p.transitionToConnected()
	now := p.clock.Now()
	delay := p.backoff.Delay(p.failures, p.interval)
	p.send(DataMsg{
		Resource:      res,
		Tenant:        p.tenant,
		ResourceLabel: p.resource,
		At:            now,
		NextAt:        now.Add(delay),
	})
	return delay, true
}

// transitionToConnected emits the recovery BackendStatusMsg only on a real
// transition. Detail/Failures/NextAt stay zero so the ping can't hijack consumers
// that read NextAt for their own success cadence (DataMsg drives that UI).
func (p *Poller) transitionToConnected() {
	if p.state == header.ConnConnected {
		return
	}
	p.state = header.ConnConnected
	p.send(BackendStatusMsg{Tenant: p.tenant, State: header.ConnConnected})
}

// Delay returns the wait before the next tick: interval ±jitter on success,
// exponential growth capped at CapMultiplier × interval ±jitter on failure.
func (b Backoff) Delay(failures int, interval time.Duration) time.Duration {
	if failures == 0 {
		return b.applyJitter(interval)
	}
	d := b.Base << min(failures-1, 30)
	maxDelay := time.Duration(b.CapMultiplier) * interval
	if d > maxDelay {
		d = maxDelay
	}
	return b.applyJitter(d)
}

// applyJitter perturbs d by ±JitterFraction; zero returns d unchanged.
func (b Backoff) applyJitter(d time.Duration) time.Duration {
	if b.JitterFraction <= 0 {
		return d
	}
	span := float64(d) * b.JitterFraction
	delta := (rand.Float64()*2 - 1) * span //nolint:gosec // jitter only
	out := time.Duration(float64(d) + delta)
	if out < 0 {
		// A negative duration would never fire.
		return 0
	}
	return out
}

// stateFromErr maps a backend error into a connection state. The
// mapping is coarse: anything that satisfies ErrUnreachable maps to
// Unreachable; anything else (auth, transient) maps to Degraded.
func stateFromErr(err error) header.ConnState {
	if errors.Is(err, backend.ErrUnreachable) {
		return header.ConnUnreachable
	}
	return header.ConnDegraded
}

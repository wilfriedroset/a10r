// SPDX-License-Identifier: Apache-2.0

// Package poll runs one (backend, resource) poll loop with
// backoff, jitter, and asymmetric connection-state emission:
// per-tick during a failure run so consumers can re-render against
// the running counter and next-attempt clock; transition-only on
// the success path because per-tick UI is already driven by
// DataMsg. See ADR-0014.
//
// The poller is goroutine-based — bubbletea v2 supports external
// goroutines via Program.Send, which is the channel the poller
// uses to publish DataMsg / BackendStatusMsg into the program
// loop. Per the project's containedctx guidance, ctx is accepted
// as a Start argument and never stored; only the derived cancel
// func lives on the struct.
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

// minPollInterval is the floor New applies to opts.Interval. The
// config layer should already have validated upstream — this is a
// final safety net so a regression there cannot drive the loop
// faster than 100 ms × N backends, which would saturate both the
// terminal and the alertmanager without anyone noticing.
const minPollInterval = 100 * time.Millisecond

// FetchFunc is the per-tick callback that performs the actual
// backend work. It returns a typed payload (the poller treats it
// as opaque any) plus an optional error. ctx propagation is the
// caller's contract — the poller passes its own cancellable ctx
// through.
type FetchFunc func(ctx context.Context) (any, error)

// SendFunc publishes a tea.Msg into the bubbletea program loop.
// In production this is *tea.Program.Send; in tests it's a small
// channel-backed recorder.
type SendFunc func(tea.Msg)

// DataMsg is emitted on every successful poll tick. Resource is
// the typed payload returned by FetchFunc — pages type-assert it
// to the shape they expect ([]backend.Alert etc.). Tenant is the
// per-tenant tag the poller was constructed with so a multi-tenant
// page can route the result. ResourceLabel is the resource bucket
// the poller was constructed with ("alerts", "silences", …) — it
// lets the App-level snapshot cache key by (label, tenant) so a
// freshly-pushed page can hydrate from the cached payload of its
// own resource without receiving payloads it doesn't care about.
// Empty in tests that build DataMsg by hand. At marks when the
// fetch completed (clock.Now after the fetch) so pages can render
// "last refresh 5s ago" without a parallel ticker. NextAt marks
// when the next tick is scheduled — pages render the countdown
// straight from it. Both are zero-valued in tests that don't care.
type DataMsg struct {
	Resource      any
	Tenant        string
	ResourceLabel string
	At            time.Time
	NextAt        time.Time
}

// BackendStatusMsg is emitted asymmetrically: per failed tick so
// downstream consumers can re-render against a fresh next-attempt
// clock, but transition-only on the success path because DataMsg
// already drives the success-case UI. See ADR-0014.
//
// Detail carries a short, operator-facing error message — empty
// on transitions to ConnConnected (the success case has no
// diagnostic to show). The string is the err.Error() form
// returned by the fetch and is therefore subject to redaction at
// the underlying transport layer (ADR 0008): no caller may pass a
// raw bearer token here because the transport's auth wrapper
// prefixes a sanitised form upstream.
//
// Failures is the running count of consecutive failed fetches.
// Zero on recovery emissions.
//
// NextAt is the clock time the poller will attempt the next fetch
// at. Zero on recovery emissions. Computed once per tick alongside
// the loop's actual sleep so a consumer rendering "next - now"
// stays in lockstep with the goroutine's real wait.
type BackendStatusMsg struct {
	Tenant   string
	State    header.ConnState
	Detail   string
	Failures int
	NextAt   time.Time
}

// Backoff controls how the poller spaces out attempts after a
// failure. The fields are public so callers can tune per backend
// (a flaky upstream might want a longer cap) but the zero value
// produces sane defaults via DefaultBackoff.
type Backoff struct {
	// Base is the first delay after a failure.
	Base time.Duration
	// CapMultiplier caps the delay at CapMultiplier × interval.
	CapMultiplier int
	// JitterFraction is the ±fraction of the delay added as jitter
	// (0.1 = ±10%). Zero means no jitter.
	JitterFraction float64
}

// defaultBackoff is the project default: 1s base, capped at 6×
// interval, ±10% jitter — matches the k9s audit's reconnection
// cadence. A function (not a var) so callers can't accidentally
// mutate the package-level schedule.
func defaultBackoff() Backoff {
	return Backoff{
		Base:           time.Second,
		CapMultiplier:  6,
		JitterFraction: 0.1,
	}
}

// Options bundles the construction inputs. Exposed as a struct so
// the constructor stays additive: a future field (auth handle,
// per-tenant rate limit) lands without touching every test.
type Options struct {
	// Tenant is the tag baked into every emitted message so pages
	// can route by source backend.
	Tenant string
	// Resource labels what the poller fetches ("alerts", "silences",
	// "receivers", "groups"). The poller does not interpret it; the
	// wiring layer uses it to bucket pollers so manual `r` refresh
	// can target a specific resource for the active scope.
	Resource string
	// Interval is the desired success-case tick spacing. The
	// configuration default is 1 minute (see config.DefaultPollInterval);
	// the poller does NOT enforce a floor — tests use sub-second
	// intervals freely.
	Interval time.Duration
	// Fetch is the per-tick worker. Must not be nil.
	Fetch FetchFunc
	// Send publishes messages into the program loop. Must not be nil.
	Send SendFunc
	// Clock injects time. nil defaults to clock.System.
	Clock clock.Clock
	// Backoff is the failure-mode delay schedule. Zero value falls
	// back to DefaultBackoff.
	Backoff Backoff
}

// Poller is one (backend, resource) loop. Construct via New, drive
// via Start / Stop. The intended model is one goroutine per Poller:
//
//   - state, failures, and the rest of the per-tick fields are read
//     and written ONLY from the loop goroutine. Don't add an
//     external Snapshot() without first promoting them under mu.
//   - Start spawns the goroutine if no previous goroutine is alive.
//     Calling Start again while the loop is running is a no-op.
//   - Stop signals cancel and joins the goroutine before returning,
//     so a Stop → Start sequence is race-free: the new goroutine
//     starts with a clean state field set the same way New does.
type Poller struct {
	tenant   string
	resource string
	interval time.Duration
	fetch    FetchFunc
	send     SendFunc
	clock    clock.Clock
	backoff  Backoff

	// state tracks the last connection state we emitted. The
	// recovery emit gates on it (skip when already Connected);
	// failure ticks set it but never gate on it (they emit
	// per-tick regardless of prior state). Born in
	// ConnUnreachable so the cold-start success tick naturally
	// fires its first recovery emission (Connected != Unreachable).
	// Loop-goroutine only — see the type doc.
	state header.ConnState
	// failures counts consecutive errors since the last success;
	// drives the exponential backoff. Loop-goroutine only.
	failures int

	// refresh wakes the loop early when Refresh is called. Buffered
	// at 1 so a Refresh during an in-flight fetch lands a single
	// queued nudge — additional Refresh calls coalesce into the same
	// pending wake-up. Drained inside the select; never closed.
	refresh chan struct{}

	// mu protects cancel / done. The loop goroutine never touches
	// either after Start returns; only the public lifecycle methods
	// do.
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New constructs a Poller. Required fields (Resource, Interval,
// Fetch, Send) are validated; missing ones panic because they
// indicate programmer error rather than runtime conditions.
// Tenant is allowed empty for tests that don't fan out across
// backends, but Resource is mandatory: it's what lets the App-
// level snapshot cache key payloads by resource so a freshly-
// pushed page hydrates from the right bucket. A poller that
// forgot to set Resource would silently bypass the cache —
// catching it at construction beats debugging "loading…" later.
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
	// Defence in depth: floor the interval at minPollInterval. The
	// config layer should have validated this already, but a future
	// regression that passed through e.g. 50ms across 10 backends
	// would melt the operator's terminal and the upstream backend
	// before anyone noticed. Tests passing intentionally tiny
	// intervals stay above the floor.
	interval := opts.Interval
	if interval < minPollInterval {
		// Loud-but-not-fatal: log so an operator sees the silent
		// clamp instead of wondering why the configured 50ms
		// behaves like 100ms.
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
		// Cold-start sentinel: Unreachable so a first-tick success
		// trips transitionToConnected's prior-state check and the
		// initial recovery emission fires. A first-tick failure
		// emits unconditionally on the per-tick branch regardless
		// of this value.
		state: header.ConnUnreachable,
	}
}

// Tenant returns the tenant tag this poller was constructed with.
// Read-only; intended for the wiring layer that needs to bucket
// pollers by (resource, tenant) for refresh routing.
func (p *Poller) Tenant() string { return p.tenant }

// Resource returns the resource label this poller was constructed
// with. Read-only; intended for the wiring layer.
func (p *Poller) Resource() string { return p.resource }

// Refresh nudges the loop to fetch immediately, replacing the
// next scheduled wake-up. Non-blocking: a Refresh during an
// in-flight fetch coalesces into the buffered slot, so several
// presses of `r` in quick succession trigger a single early tick.
// Failure backoff is left intact — a manual refresh against a
// down backend doesn't reset the failure counter.
func (p *Poller) Refresh() {
	select {
	case p.refresh <- struct{}{}:
	default:
		// Slot already full; the queued nudge will fire on the
		// next select pass.
	}
}

// Start spawns the polling goroutine. Cancellable via Stop or via
// the parent ctx being cancelled. Calling Start while a previous
// goroutine is still running is a no-op; Start after a successful
// Stop spawns a fresh goroutine and resets per-tick state so the
// caller sees the same behaviour as a freshly-constructed Poller.
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
	// Reset per-tick state so Stop → Start starts as if freshly
	// constructed. No data race: the previous goroutine is joined
	// in Stop before this point is reachable.
	p.state = header.ConnUnreachable
	p.failures = 0
	go func() {
		defer close(done)
		p.run(loopCtx)
	}()
}

// Stop signals the polling goroutine to exit and waits for it to
// finish. Idempotent: safe to call multiple times. Joining the
// goroutine here means Stop → Start is race-free — the new run
// begins with a clean state.
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
	// Tick immediately on start so the page doesn't wait one full
	// interval to see its first data — matches k9s "load now, then
	// poll" UX.
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
			// Manual refresh: skip the scheduled wait and tick now.
			// We deliberately do not reset p.failures — a refresh
			// against a flaky upstream shouldn't pretend the previous
			// failures didn't happen. Stop the timer so a pressed
			// refresh against a tenant in backoff doesn't leave a
			// runtime timer outstanding for the full backoff window.
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

// tickOnce performs one fetch, emits the resulting messages, and
// returns the delay before the next scheduled tick. The same delay
// value is published as DataMsg.NextAt so the on-screen countdown
// stays in lockstep with the loop's actual sleep — computing
// Backoff.Delay twice would produce two different jitter draws
// and drift the displayed "next refresh in N" off the real wait.
//
// Returns (_, false) when ctx is done so the loop can exit
// promptly. A refresh nudge that landed during the fetch is
// drained here — we already satisfied it with this tick.
func (p *Poller) tickOnce(ctx context.Context) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	res, err := p.fetch(ctx)
	if ctx.Err() != nil {
		// ctx cancelled mid-fetch — drop the result silently and
		// exit. Don't transition the state because the user asked
		// us to stop, not because the backend is down.
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

// transitionToConnected emits the recovery BackendStatusMsg only
// when the prior state was not already connected — the cold-start
// state is ConnUnreachable, so the first successful tick fires the
// emission naturally. Detail / Failures / NextAt are zero: the
// success-path UI is driven by DataMsg, so the recovery ping
// carries no diagnostic and must not hijack downstream consumers
// that read NextAt for their own success-case cadence.
func (p *Poller) transitionToConnected() {
	if p.state == header.ConnConnected {
		return
	}
	p.state = header.ConnConnected
	p.send(BackendStatusMsg{Tenant: p.tenant, State: header.ConnConnected})
}

// Delay returns the wait before the next scheduled tick.
// failures==0 (success path) yields the supplied interval ±jitter.
// failures>0 produces exponential growth (base, base×2, base×4, …)
// capped at CapMultiplier × interval, also ±jitter. Goroutine-safe
// only insofar as math/rand/v2 is — Backoff itself carries no
// mutable state.
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

// applyJitter perturbs d by ±JitterFraction. Zero JitterFraction
// returns d unchanged. Random source is package-level rand/v2 —
// no caller varies on randomness today, and the production jitter
// envelope is verified end-to-end by TestPoller_JitterEnvelope.
func (b Backoff) applyJitter(d time.Duration) time.Duration {
	if b.JitterFraction <= 0 {
		return d
	}
	span := float64(d) * b.JitterFraction
	// rand/v2 is goroutine-safe per docs; one source per Go runtime.
	delta := (rand.Float64()*2 - 1) * span //nolint:gosec // jitter only
	out := time.Duration(float64(d) + delta)
	if out < 0 {
		// Defensive: a negative duration would never fire.
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

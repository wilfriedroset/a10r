// SPDX-License-Identifier: Apache-2.0

// Package poll runs one (backend, resource) poll loop with
// backoff, jitter, and transition-only connection-state emission.
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
	"math/rand/v2"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/header"
)

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

// BackendStatusMsg is emitted only when the connection state
// actually changes. This avoids re-rendering the header on every
// successful tick when nothing visually changed.
type BackendStatusMsg struct {
	Tenant string
	State  header.ConnState
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

// defaultBackoff is the v0.1 default: 1s base, capped at 6×
// interval, ±10% jitter — matches the plan and matches the k9s
// audit's reconnection cadence. A function (not a var) so callers
// can't accidentally mutate the package-level schedule.
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
	// Interval is the desired success-case tick spacing. Per I3 the
	// configuration default is 1 minute; the poller does NOT enforce
	// a floor — tests use sub-second intervals freely.
	Interval time.Duration
	// Fetch is the per-tick worker. Must not be nil.
	Fetch FetchFunc
	// Send publishes messages into the program loop. Must not be nil.
	Send SendFunc
	// Clock injects time. nil defaults to SystemClock.
	Clock Clock
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
	clock    Clock
	backoff  Backoff

	// state tracks the last connection state we emitted so we only
	// publish BackendStatusMsg on real transitions. Loop-goroutine
	// only — see the type doc.
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
	clock := opts.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	bo := opts.Backoff
	if bo.Base == 0 {
		bo = defaultBackoff()
	}
	return &Poller{
		tenant:   opts.Tenant,
		resource: opts.Resource,
		interval: opts.Interval,
		fetch:    opts.Fetch,
		send:     opts.Send,
		clock:    clock,
		backoff:  bo,
		refresh:  make(chan struct{}, 1),
		// Start as Unreachable so the very first successful tick
		// emits a transition to Connected — pages get a clean
		// "we're online" signal even on cold start.
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
// nextDelay twice would produce two different jitter draws and
// drift the displayed "next refresh in N" off the real wait.
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
		p.transition(stateFromErr(err))
		return p.nextDelay(), true
	}
	p.failures = 0
	p.transition(header.ConnConnected)
	now := p.clock.Now()
	delay := p.nextDelay()
	p.send(DataMsg{
		Resource:      res,
		Tenant:        p.tenant,
		ResourceLabel: p.resource,
		At:            now,
		NextAt:        now.Add(delay),
	})
	return delay, true
}

// transition emits a BackendStatusMsg only when state actually
// changes from the last emitted value.
func (p *Poller) transition(next header.ConnState) {
	if next == p.state {
		return
	}
	p.state = next
	p.send(BackendStatusMsg{Tenant: p.tenant, State: next})
}

// nextDelay returns the wait before the next tick. Success → full
// interval. Failure → exponential backoff capped at
// CapMultiplier × interval, with ±JitterFraction noise.
func (p *Poller) nextDelay() time.Duration {
	if p.failures == 0 {
		return p.applyJitter(p.interval)
	}
	// Exponential growth: base, base×2, base×4, …
	d := p.backoff.Base << min(p.failures-1, 30)
	maxDelay := time.Duration(p.backoff.CapMultiplier) * p.interval
	if d > maxDelay {
		d = maxDelay
	}
	return p.applyJitter(d)
}

// applyJitter perturbs d by ±JitterFraction. Zero JitterFraction
// returns d unchanged.
func (p *Poller) applyJitter(d time.Duration) time.Duration {
	if p.backoff.JitterFraction <= 0 {
		return d
	}
	span := float64(d) * p.backoff.JitterFraction
	// rand/v2 is goroutine-safe per docs; one source per Go runtime.
	delta := (rand.Float64()*2 - 1) * span //nolint:gosec // jitter only
	out := time.Duration(float64(d) + delta)
	if out < 0 {
		// Defensive: a negative duration would never fire.
		return 0
	}
	return out
}

// stateFromErr maps a backend error into a connection state. v0.1
// keeps it coarse: anything that satisfies ErrUnreachable maps to
// Unreachable; anything else (auth, transient) maps to Degraded.
func stateFromErr(err error) header.ConnState {
	if errors.Is(err, backend.ErrUnreachable) {
		return header.ConnUnreachable
	}
	return header.ConnDegraded
}

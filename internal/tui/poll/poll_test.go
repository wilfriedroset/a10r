// SPDX-License-Identifier: Apache-2.0

package poll

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/header"
)

// fakeClock is a deterministic Clock for backoff / interval tests.
// After calls return a channel that the test fires by calling
// Advance — no real time passes. Now returns a fixed timestamp
// because the poller only uses it for jitter seeding (which we
// disable in tests anyway).
type fakeClock struct {
	mu      sync.Mutex
	pending []chan<- time.Time
	delays  []time.Duration
	now     time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.pending = append(c.pending, ch)
	c.delays = append(c.delays, d)
	return ch
}

// NewTimer returns a stoppable Timer that observes the same fire-on-
// fireNext discipline as After. Stop drops the pending entry from the
// scheduler so cancelled timers don't get force-fired by a later
// fireNext call (matches the production NewTimer contract).
func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.pending = append(c.pending, ch)
	c.delays = append(c.delays, d)
	return &fakeTimer{clock: c, ch: ch}
}

// fakeTimer is the channel-backed Timer the fake clock returns from
// NewTimer. Stop walks the parent's pending list to drop this
// timer's entry so a fireNext after Stop fires the next live timer
// instead of a cancelled one.
type fakeTimer struct {
	clock *fakeClock
	ch    chan time.Time
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	for i, ch := range t.clock.pending {
		if ch == t.ch {
			t.clock.pending = append(t.clock.pending[:i], t.clock.pending[i+1:]...)
			t.clock.delays = append(t.clock.delays[:i], t.clock.delays[i+1:]...)
			return true
		}
	}
	return false
}

// fireNext fires the oldest pending After channel and returns the
// duration the poller asked for, so tests can assert the schedule.
// Blocks (with a generous timeout) until at least one pending
// channel exists — the poller's goroutine may not have reached the
// After call yet.
func (c *fakeClock) fireNext(t *testing.T) time.Duration {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		if len(c.pending) > 0 {
			ch := c.pending[0]
			d := c.delays[0]
			c.pending = c.pending[1:]
			c.delays = c.delays[1:]
			c.now = c.now.Add(d)
			c.mu.Unlock()
			ch <- c.now
			return d
		}
		c.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("fakeClock.fireNext timed out waiting for After call")
		}
		time.Sleep(time.Millisecond)
	}
}

// recorder collects messages sent by the poller. Channel-backed so
// tests can wait deterministically on each emission.
type recorder struct {
	msgs chan tea.Msg
}

func newRecorder() *recorder {
	return &recorder{msgs: make(chan tea.Msg, 32)}
}

func (r *recorder) Send(m tea.Msg) { r.msgs <- m }

// next returns the next message or fails if none arrives in time.
func (r *recorder) next(t *testing.T) tea.Msg {
	t.Helper()
	select {
	case m := <-r.msgs:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("recorder.next timed out — no message emitted")
	}
	return nil
}

// drainNoMore asserts that no further messages are queued. The
// deadline is loose enough to ride out a contended CI runner while
// still not slowing the suite materially.
func (r *recorder) drainNoMore(t *testing.T) {
	t.Helper()
	select {
	case m := <-r.msgs:
		t.Fatalf("unexpected extra message: %#v", m)
	case <-time.After(250 * time.Millisecond):
	}
}

// noJitter is the test-friendly Backoff — base 1s, cap 6×, no
// jitter so durations are deterministic.
var noJitter = Backoff{
	Base:           time.Second,
	CapMultiplier:  6,
	JitterFraction: 0,
}

// stubFetch is the placeholder FetchFunc tests use when the
// payload doesn't matter — a single helper avoids duplicate
// "return X, nil" literals across the file.
func stubFetch(_ context.Context) (any, error) { return "ok", nil }

func TestPoller_FirstTickFiresImmediately(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 10 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return "payload", nil
		},
		Send:    rec.Send,
		Clock:   newFakeClock(),
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// First emission: connected transition (cold start was Unreachable).
	status, ok := rec.next(t).(BackendStatusMsg)
	require.True(t, ok, "first message must be the connected transition")
	require.Equal(t, header.ConnConnected, status.State)
	require.Equal(t, "prod", status.Tenant)

	// Second emission: the data payload.
	data, ok := rec.next(t).(DataMsg)
	require.True(t, ok)
	require.Equal(t, "payload", data.Resource)
	require.Equal(t, "prod", data.Tenant)
	require.Equal(t, "alerts", data.ResourceLabel,
		"DataMsg must carry the constructor's Resource label so the App-level cache can key by (label, tenant)")
}

func TestPoller_TickIntervalRespected(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 5 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return 1, nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// First tick is immediate: connected + data.
	_ = rec.next(t)
	_ = rec.next(t)

	// Second tick must wait Interval. Fire the After channel.
	delay := clock.fireNext(t)
	require.Equal(t, 5*time.Second, delay,
		"successful tick must schedule the next at interval")

	// Second tick: only DataMsg (no transition; still connected).
	msg := rec.next(t)
	_, ok := msg.(DataMsg)
	require.True(t, ok, "second tick on a connected backend emits DataMsg only")
}

func TestPoller_TransitionEmittedOnlyOnChange(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return 1, nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Tick 1: connected transition + DataMsg.
	require.IsType(t, BackendStatusMsg{}, rec.next(t))
	require.IsType(t, DataMsg{}, rec.next(t))

	// Tick 2: only DataMsg (state didn't change).
	clock.fireNext(t)
	require.IsType(t, DataMsg{}, rec.next(t))
	rec.drainNoMore(t)
}

func TestPoller_FirstTickFailureSchedulesBackoffBase(t *testing.T) {
	t.Parallel()

	// Locks the cold-start invariant: the poller is born in
	// ConnUnreachable, so a first-tick failure with err=Unreachable
	// is NOT a transition and emits no BackendStatusMsg. The next
	// fetch is scheduled at exactly Backoff.Base.
	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 30 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return nil, backend.ErrUnreachable
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// No transition emitted on the cold-start failure.
	rec.drainNoMore(t)
	// Next attempt is scheduled at Base, regardless of Interval.
	require.Equal(t, time.Second, clock.fireNext(t),
		"first failure must schedule next at exactly Backoff.Base")
}

func TestPoller_FailureClassTransitions(t *testing.T) {
	t.Parallel()

	// A failure of one class (Unreachable) followed by a failure of
	// another class (auth → Degraded) must emit a transition mid-
	// backoff, even though the poller is still failing.
	rec := newRecorder()
	clock := newFakeClock()
	var tick atomic.Int32
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 30 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			if tick.Add(1) == 1 {
				return nil, backend.ErrUnreachable
			}
			return nil, backend.ErrUnauthorized
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Tick 1 (Unreachable) — no transition (cold-start invariant).
	rec.drainNoMore(t)
	clock.fireNext(t)

	// Tick 2 (Unauthorized → Degraded) — transition emitted because
	// the state actually changed.
	status, ok := rec.next(t).(BackendStatusMsg)
	require.True(t, ok)
	require.Equal(t, header.ConnDegraded, status.State)
}

func TestPoller_BackoffOnFailure(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	var calls atomic.Int32
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 5 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			calls.Add(1)
			return nil, backend.ErrUnreachable
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Cold-start invariant: failure at state=Unreachable is not a
	// transition, so no BackendStatusMsg is emitted on the first
	// failed tick. See TestPoller_FirstTickFailureSchedulesBackoffBase.
	rec.drainNoMore(t)

	// Second attempt: backoff base = 1s.
	delay := clock.fireNext(t)
	require.Equal(t, time.Second, delay,
		"first failure must trigger backoff = base (1s)")

	// Third attempt: 2s.
	delay = clock.fireNext(t)
	require.Equal(t, 2*time.Second, delay)

	// Fourth attempt: 4s.
	delay = clock.fireNext(t)
	require.Equal(t, 4*time.Second, delay)
}

func TestPoller_BackoffCapped(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 5 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return nil, backend.ErrUnreachable
		},
		Send:  rec.Send,
		Clock: clock,
		Backoff: Backoff{
			Base:          time.Second,
			CapMultiplier: 2, // cap = 10s
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Walk a handful of failure cycles. After the cap kicks in, the
	// delay should plateau at Cap = 2 × Interval = 10s.
	delays := make([]time.Duration, 0, 6)
	for range 6 {
		delays = append(delays, clock.fireNext(t))
	}
	// Last should be 10s (capped).
	require.LessOrEqual(t, delays[len(delays)-1], 10*time.Second)
	require.Equal(t, 10*time.Second, delays[len(delays)-1])
}

func TestPoller_ResetBackoffOnSuccess(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	var failNext atomic.Int32
	failNext.Store(1)
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 5 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			if failNext.Add(-1) >= 0 {
				return nil, backend.ErrUnreachable
			}
			return "ok", nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Tick 1: failure (cold start state = Unreachable, no transition
	// emitted, no DataMsg).
	rec.drainNoMore(t)

	// Tick 2: backoff = 1s, then success → transition + DataMsg.
	require.Equal(t, time.Second, clock.fireNext(t))
	require.IsType(t, BackendStatusMsg{}, rec.next(t))
	require.IsType(t, DataMsg{}, rec.next(t))

	// Tick 3: success-after-success uses full interval (backoff reset).
	require.Equal(t, 5*time.Second, clock.fireNext(t))
}

func TestPoller_TransitionDegradedOnAuthError(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return nil, backend.ErrUnauthorized
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Cold start was Unreachable → degraded transition emitted.
	status, ok := rec.next(t).(BackendStatusMsg)
	require.True(t, ok)
	require.Equal(t, header.ConnDegraded, status.State,
		"auth error must map to Degraded, not Unreachable")
}

func TestPoller_CtxCancelHaltsLoop(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	fetchedAtLeastOnce := make(chan struct{}, 1)
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Hour,
		Fetch: func(_ context.Context) (any, error) {
			select {
			case fetchedAtLeastOnce <- struct{}{}:
			default:
			}
			return 1, nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	p.Start(ctx)

	// Wait for the first fetch to happen, then drain its emitted
	// transition + data messages.
	<-fetchedAtLeastOnce
	_ = rec.next(t)
	_ = rec.next(t)

	// Cancel and assert no further fetches happen even after a
	// generous wait. The poller's loop must exit on ctx.Done()
	// without firing another After channel.
	cancel()
	rec.drainNoMore(t)
}

func TestPoller_StopIdempotent(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Hour,
		Fetch: func(_ context.Context) (any, error) {
			return 1, nil
		},
		Send:    rec.Send,
		Clock:   newFakeClock(),
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	p.Stop()
	p.Stop() // must not panic
}

func TestPoller_StopThenStartResetsState(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: 5 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return "ok", nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Run once: connected transition + DataMsg.
	p.Start(ctx)
	require.IsType(t, BackendStatusMsg{}, rec.next(t))
	require.IsType(t, DataMsg{}, rec.next(t))

	// Stop joins the goroutine deterministically so the next Start
	// is race-free.
	p.Stop()
	rec.drainNoMore(t)

	// Restart: the poller's per-tick state was reset, so the first
	// successful tick emits a fresh ConnConnected transition again.
	p.Start(ctx)
	defer p.Stop()
	require.IsType(t, BackendStatusMsg{}, rec.next(t),
		"Stop → Start must re-emit the connected transition")
	require.IsType(t, DataMsg{}, rec.next(t))
}

func TestPoller_DoubleStartIsNoOp(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	var calls atomic.Int32
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Hour,
		Fetch: func(_ context.Context) (any, error) {
			calls.Add(1)
			return 1, nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	p.Start(ctx) // should be a no-op
	defer p.Stop()

	// Drain first-tick emissions and assert only one fetch happened.
	_ = rec.next(t)
	_ = rec.next(t)
	rec.drainNoMore(t)
	require.Equal(t, int32(1), calls.Load(),
		"double Start must not double-spawn the goroutine")
}

func TestPoller_JitterEnvelope(t *testing.T) {
	t.Parallel()

	// With jitter at 10% and a base interval of 1s, every measured
	// delay must lie within [0.9s, 1.1s]. Sample many cycles to
	// catch a regression that drops jitter math precision.
	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return 1, nil
		},
		Send:  rec.Send,
		Clock: clock,
		Backoff: Backoff{
			Base:           time.Second,
			CapMultiplier:  6,
			JitterFraction: 0.1,
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// First tick is immediate, drain its messages.
	_ = rec.next(t)
	_ = rec.next(t)

	for range 20 {
		d := clock.fireNext(t)
		require.GreaterOrEqual(t, d, 900*time.Millisecond)
		require.LessOrEqual(t, d, 1100*time.Millisecond)
		_ = rec.next(t) // DataMsg from this tick
	}
}

func TestStateFromErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want header.ConnState
	}{
		{name: "unreachable sentinel", err: backend.ErrUnreachable, want: header.ConnUnreachable},
		{name: "wrapped unreachable", err: errors.Join(errors.New("ctx"), backend.ErrUnreachable), want: header.ConnUnreachable},
		{name: "auth error", err: backend.ErrUnauthorized, want: header.ConnDegraded},
		{name: "generic error", err: errors.New("oops"), want: header.ConnDegraded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, stateFromErr(tc.err))
		})
	}
}

func TestPoller_DataMsgCarriesAtAndNextAt(t *testing.T) {
	t.Parallel()

	// A successful tick stamps DataMsg.At with the clock's Now and
	// DataMsg.NextAt with At + the schedule the loop is about to
	// sleep for. Pages render "last refresh / next refresh in"
	// straight from these — divergence between NextAt and the real
	// wait would drift the on-screen countdown off the loop.
	rec := newRecorder()
	clock := newFakeClock()
	startNow := clock.Now()
	p := New(Options{
		Tenant:   "prod",
		Resource: "silences",
		Interval: 30 * time.Second,
		Fetch: func(_ context.Context) (any, error) {
			return "payload", nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	require.IsType(t, BackendStatusMsg{}, rec.next(t))
	data, ok := rec.next(t).(DataMsg)
	require.True(t, ok)
	require.Equal(t, "prod", data.Tenant)
	require.Equal(t, startNow, data.At, "At must be the post-fetch clock.Now")
	require.Equal(t, startNow.Add(30*time.Second), data.NextAt,
		"NextAt must equal At plus the loop's actual sleep")
}

func TestPoller_RefreshTriggersImmediateTick(t *testing.T) {
	t.Parallel()

	// Refresh wakes the loop without firing the scheduled After
	// channel. Asserts (a) a fresh DataMsg arrives without anyone
	// calling clock.fireNext, and (b) the originally-pending After
	// is left in place (the test never tries to consume it; the
	// drain on Stop covers cleanup).
	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "silences",
		Interval: time.Hour, // long enough that After never fires in this test
		Fetch: func(_ context.Context) (any, error) {
			return "payload", nil
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Drain the immediate cold-start tick.
	require.IsType(t, BackendStatusMsg{}, rec.next(t))
	require.IsType(t, DataMsg{}, rec.next(t))

	// Wait for the loop to enter its select on the After channel.
	// fakeClock records the After call when the loop reaches it;
	// we poll until at least one pending channel exists so the
	// Refresh below races against an established sleep, not a
	// pre-After window.
	deadline := time.Now().Add(2 * time.Second)
	for {
		clock.mu.Lock()
		pending := len(clock.pending)
		clock.mu.Unlock()
		if pending >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop never reached After before the test deadline")
		}
		time.Sleep(time.Millisecond)
	}

	// Refresh nudges the loop early. No fireNext call.
	p.Refresh()
	data, ok := rec.next(t).(DataMsg)
	require.True(t, ok, "Refresh must produce a DataMsg without firing the schedule")
	require.Equal(t, "payload", data.Resource)
}

// TestPoller_RefreshDropsPendingTimer locks F10. Mashing Refresh
// while a tenant is in backoff must not leak a pending timer per
// press — the loop releases the previous Timer via Stop before
// arming the next one. Asserted by counting pending entries on the
// fake clock after a refresh storm: each refresh consumes one
// pending entry (the live timer), arms one more, so the count is
// stable at 1.
func TestPoller_RefreshDropsPendingTimer(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "alerts",
		Interval: time.Hour,
		Fetch:    func(_ context.Context) (any, error) { return "payload", nil },
		Send:     rec.Send,
		Clock:    clock,
		Backoff:  noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Drain cold-start emissions.
	require.IsType(t, BackendStatusMsg{}, rec.next(t))
	require.IsType(t, DataMsg{}, rec.next(t))

	// Wait until the loop enters its select with a timer armed.
	deadline := time.Now().Add(2 * time.Second)
	for {
		clock.mu.Lock()
		pending := len(clock.pending)
		clock.mu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop never armed the initial timer")
		}
		time.Sleep(time.Millisecond)
	}

	for range 10 {
		p.Refresh()
		// Each refresh produces one DataMsg; drain so the loop
		// re-arms a fresh timer before the next refresh.
		require.IsType(t, DataMsg{}, rec.next(t))
	}

	// After 10 refreshes, the loop should still hold exactly one
	// pending timer — the freshly armed one. Every prior timer was
	// stopped before being replaced.
	deadline = time.Now().Add(2 * time.Second)
	for {
		clock.mu.Lock()
		pending := len(clock.pending)
		clock.mu.Unlock()
		if pending == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected exactly 1 pending timer after refresh storm, got %d", pending)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPoller_RefreshIsNonBlocking(t *testing.T) {
	t.Parallel()

	// Refresh must never block the caller. With no goroutine
	// running (Start not called) the buffered slot fills on the
	// first call and subsequent calls coalesce into the same
	// pending nudge — verifying the default-case branch of the
	// select. Test passes by completing within the deadline.
	p := New(Options{
		Tenant:   "prod",
		Resource: "silences",
		Interval: time.Hour,
		Fetch:    stubFetch,
		Send:     func(tea.Msg) {},
		Clock:    newFakeClock(),
		Backoff:  noJitter,
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			p.Refresh()
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Refresh blocked — buffer / default-branch broken")
	}
}

func TestPoller_RefreshDoesNotResetFailures(t *testing.T) {
	t.Parallel()

	// Refresh against a flaky upstream must keep the backoff
	// counter intact — the manual nudge picks "tick now" but the
	// fact that the previous attempts failed is still true. After
	// one failed cold-start tick + one Refresh-driven failed tick,
	// the next After delay must reflect failures=2 (i.e. 2s, not
	// the post-success interval).
	rec := newRecorder()
	clock := newFakeClock()
	p := New(Options{
		Tenant:   "prod",
		Resource: "silences",
		Interval: time.Hour,
		Fetch: func(_ context.Context) (any, error) {
			return nil, backend.ErrUnreachable
		},
		Send:    rec.Send,
		Clock:   clock,
		Backoff: noJitter,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	// Wait for the After-after-cold-start to register.
	deadline := time.Now().Add(2 * time.Second)
	for {
		clock.mu.Lock()
		pending := len(clock.pending)
		firstDelay := time.Duration(0)
		if pending >= 1 {
			firstDelay = clock.delays[0]
		}
		clock.mu.Unlock()
		if firstDelay == time.Second { // base = 1s after first failure
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop never scheduled the post-failure backoff")
		}
		time.Sleep(time.Millisecond)
	}
	// Refresh: triggers a second failed tick without firing After.
	p.Refresh()
	// Wait for the next After to be scheduled — its delay reflects
	// failures=2 (2 × base = 2s), not a post-refresh reset.
	deadline = time.Now().Add(2 * time.Second)
	for {
		clock.mu.Lock()
		pending := len(clock.pending)
		latest := time.Duration(0)
		if pending >= 1 {
			latest = clock.delays[len(clock.delays)-1]
		}
		clock.mu.Unlock()
		if latest == 2*time.Second {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected post-Refresh delay 2s, got %v with %d pending", latest, pending)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPoller_AccessorsExposeTagFields(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Tenant:   "prod",
		Resource: "silences",
		Interval: time.Second,
		Fetch:    stubFetch,
		Send:     func(tea.Msg) {},
		Backoff:  noJitter,
	})
	require.Equal(t, "prod", p.Tenant())
	require.Equal(t, "silences", p.Resource())
}

func TestNew_PanicsOnInvalidOptions(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, "poll.New: Interval must be positive", func() {
		New(Options{Interval: 0, Resource: "alerts", Fetch: stubFetch, Send: func(tea.Msg) {}})
	})
	require.PanicsWithValue(t, "poll.New: Resource must not be empty", func() {
		New(Options{Interval: time.Second, Fetch: stubFetch, Send: func(tea.Msg) {}})
	})
	require.PanicsWithValue(t, "poll.New: Fetch must not be nil", func() {
		New(Options{Interval: time.Second, Resource: "alerts", Send: func(tea.Msg) {}})
	})
	require.PanicsWithValue(t, "poll.New: Send must not be nil", func() {
		New(Options{Interval: time.Second, Resource: "alerts", Fetch: stubFetch})
	})
}

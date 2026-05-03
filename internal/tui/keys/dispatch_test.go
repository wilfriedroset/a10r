// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"
)

// fakeClock is a deterministic Clock for chord-timing tests.
type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// recorder wraps a counter so tests can assert which handler ran.
type recorder struct {
	count atomic.Int32
	last  atomic.Pointer[string]
}

func (r *recorder) handler(label string) Handler {
	return func() tea.Cmd {
		r.count.Add(1)
		s := label
		r.last.Store(&s)
		return nil
	}
}

func (r *recorder) lastLabel() string {
	if p := r.last.Load(); p != nil {
		return *p
	}
	return ""
}

func TestDispatch_CtrlBackslashRoutesGlobal(t *testing.T) {
	t.Parallel()

	// `Ctrl+\` is the global "clear marks" binding registered by
	// cmd/tui.go at LayerGlobal. The dispatcher itself doesn't
	// know about ClearMarksMsg — it just dispatches whatever
	// handler was registered. This test pins the routing contract:
	// a handler bound at LayerGlobal under the App's normalized
	// key string fires when that exact key arrives, without chord
	// buffering. The dispatcher's lookup is a plain map read so
	// case must match exactly — every Ctrl binding in the codebase
	// uses the TitleCase `Ctrl+...` shape that `normalizeKey`
	// emits (see internal/tui/app/keys.go:69).
	d := New(newFakeClock())
	r := &recorder{}
	d.Set(LayerGlobal, "Ctrl+\\", r.handler("clear-marks"))

	consumed, cmd := d.Dispatch("Ctrl+\\")
	require.True(t, consumed, "Ctrl+\\ at LayerGlobal must be consumed")
	require.Nil(t, cmd, "the registered handler returns nil cmd in this fake")
	require.Equal(t, int32(1), r.count.Load())
	require.Equal(t, "clear-marks", r.lastLabel())
}

func TestDispatch_Empty(t *testing.T) {
	t.Parallel()

	d := New(newFakeClock())
	consumed, cmd := d.Dispatch("s")
	require.False(t, consumed, "no bindings → unconsumed")
	require.Nil(t, cmd)
}

func TestDispatch_LayerPrecedence(t *testing.T) {
	t.Parallel()

	// Same key registered at every layer; modal must win.
	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerGlobal, "Esc", r.handler("global"))
	d.Set(LayerTable, "Esc", r.handler("table"))
	d.Set(LayerView, "Esc", r.handler("view"))
	d.Set(LayerPrompt, "Esc", r.handler("prompt"))
	d.Set(LayerModal, "Esc", r.handler("modal"))

	consumed, cmd := d.Dispatch("Esc")
	require.True(t, consumed)
	require.Nil(t, cmd, "handler returned nil cmd")
	// Drive the handler so the recorder fires.
	require.Equal(t, "modal", r.lastLabel(),
		"modal layer must beat every layer below it")
	require.EqualValues(t, 1, r.count.Load())
}

func TestDispatch_TableLosesToModal(t *testing.T) {
	t.Parallel()

	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerTable, "j", r.handler("table"))
	d.Set(LayerModal, "j", r.handler("modal"))

	consumed, _ := d.Dispatch("j")
	require.True(t, consumed)
	require.Equal(t, "modal", r.lastLabel())
}

func TestDispatch_GlobalReachedWhenNoHigherLayerBinds(t *testing.T) {
	t.Parallel()

	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerGlobal, "?", r.handler("global"))

	consumed, _ := d.Dispatch("?")
	require.True(t, consumed)
	require.Equal(t, "global", r.lastLabel())
}

func TestDispatch_UnboundFallsThrough(t *testing.T) {
	t.Parallel()

	d := New(newFakeClock())
	d.Set(LayerGlobal, "?", func() tea.Cmd { return nil })

	consumed, cmd := d.Dispatch("zZz-not-bound")
	require.False(t, consumed)
	require.Nil(t, cmd)
}

func TestDispatch_ChordCompletesWithinWindow(t *testing.T) {
	t.Parallel()

	r := &recorder{}
	clock := newFakeClock()
	d := New(clock)
	d.Set(LayerTable, "gg", r.handler("first row"))

	// First g → chord pending, schedules a tick.
	consumed, cmd := d.Dispatch("g")
	require.True(t, consumed, "first g must be consumed as a chord prefix")
	require.NotNil(t, cmd, "chord prefix must schedule a tea.Tick")
	require.EqualValues(t, 0, r.count.Load(), "handler must not fire on first g")

	// 499 ms later (still inside the window) → second g completes chord.
	clock.advance(499 * time.Millisecond)
	consumed, _ = d.Dispatch("g")
	require.True(t, consumed)
	require.Equal(t, "first row", r.lastLabel())
	require.EqualValues(t, 1, r.count.Load())
}

func TestDispatch_ChordExpiresAfterWindow(t *testing.T) {
	t.Parallel()

	r := &recorder{}
	clock := newFakeClock()
	d := New(clock)
	d.Set(LayerTable, "gg", r.handler("first row"))

	_, _ = d.Dispatch("g")

	// 501 ms later → outside the window. Second g starts a NEW
	// chord rather than completing the first.
	clock.advance(501 * time.Millisecond)
	consumed, cmd := d.Dispatch("g")
	require.True(t, consumed, "second g must still be consumed (it starts a new chord)")
	require.NotNil(t, cmd, "the new chord schedules its own tick")
	require.EqualValues(t, 0, r.count.Load(),
		"the original chord must NOT have completed retroactively")
}

func TestDispatch_ChordPrefixWithoutCompletionFollowedByDifferentKey(t *testing.T) {
	t.Parallel()

	// User presses g, then within the window presses a different
	// key (not g). The chord prefix is dropped and the new key
	// dispatches normally.
	r := &recorder{}
	clock := newFakeClock()
	d := New(clock)
	d.Set(LayerTable, "gg", r.handler("first row"))
	d.Set(LayerGlobal, "j", r.handler("down"))

	_, _ = d.Dispatch("g")
	clock.advance(100 * time.Millisecond)
	consumed, _ := d.Dispatch("j")
	require.True(t, consumed)
	require.Equal(t, "down", r.lastLabel(),
		"chord prefix dropped when followed by an unrelated key")
}

func TestDispatch_ChordPrefixIgnoredWhenNoChordBound(t *testing.T) {
	t.Parallel()

	// `g` is a chord prefix in the const map, but only fires the
	// chord buffer when a `gg` (or other g-prefixed) binding exists.
	// With no such binding, `g` falls through to normal lookup —
	// which returns unbound.
	d := New(newFakeClock())
	d.Set(LayerGlobal, "?", func() tea.Cmd { return nil })

	consumed, cmd := d.Dispatch("g")
	require.False(t, consumed, "no gg binding → chord prefix is just an unbound key")
	require.Nil(t, cmd)
}

func TestHandleChordExpired_StaleTicksDiscarded(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	d := New(clock)
	d.Set(LayerTable, "gg", func() tea.Cmd { return nil })

	_, _ = d.Dispatch("g") // chord pending, expiry = +500 ms

	// A stale tick from BEFORE the current chord's expiry must be
	// ignored — happens when a chord was resolved by a key arrival
	// and a fresh chord started before the original tick fired.
	stale := ChordExpiredMsg{At: clock.t.Add(-1 * time.Second)}
	cmd := d.HandleChordExpired(stale)
	require.Nil(t, cmd)
}

func TestHandleChordExpired_NoPendingChord(t *testing.T) {
	t.Parallel()

	d := New(newFakeClock())
	cmd := d.HandleChordExpired(ChordExpiredMsg{At: time.Now()})
	require.Nil(t, cmd, "expired tick with no pending chord is a no-op")
}

func TestHandleChordExpired_RoundTripClearsState(t *testing.T) {
	t.Parallel()

	// The full round-trip path: chord prefix arrives → tea.Tick is
	// scheduled → no second key arrives → ChordExpiredMsg routes
	// back through HandleChordExpired → chord state is cleared and
	// the next "g" starts a fresh chord rather than completing the
	// previous one.
	clock := newFakeClock()
	d := New(clock)
	d.Set(LayerTable, "gg", func() tea.Cmd { return nil })

	consumed, cmd := d.Dispatch("g")
	require.True(t, consumed)
	require.NotNil(t, cmd, "chord prefix must schedule a tick")

	// Simulate the tea.Tick firing at the exact expiry time.
	expiredAt := clock.Now().Add(ChordTimeout)
	expiredCmd := d.HandleChordExpired(ChordExpiredMsg{At: expiredAt})
	require.Nil(t, expiredCmd, "v0.1 has no single-g binding to fall back to")

	// Now any g must start a fresh chord, NOT complete the original.
	r := &recorder{}
	d.Set(LayerTable, "gg", r.handler("first row"))
	clock.advance(ChordTimeout + time.Millisecond) // ensure now > original expiry
	consumed2, cmd2 := d.Dispatch("g")
	require.True(t, consumed2)
	require.NotNil(t, cmd2, "fresh g must schedule its own tick")
	require.EqualValues(t, 0, r.count.Load(),
		"the original chord must NOT have completed retroactively after expiry")
}

func TestSet_OverwritesSilently(t *testing.T) {
	t.Parallel()

	// The action.Registry handles duplicate detection; the
	// dispatcher trusts callers to validate. Last write wins.
	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerGlobal, "x", r.handler("first"))
	d.Set(LayerGlobal, "x", r.handler("second"))

	_, _ = d.Dispatch("x")
	require.Equal(t, "second", r.lastLabel())
}

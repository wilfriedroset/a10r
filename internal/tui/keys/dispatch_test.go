// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
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
	require.Nil(t, expiredCmd, "no single-g binding is wired to fall back to")

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

func TestSetAction_RegistersKeyAndExposesActionName(t *testing.T) {
	t.Parallel()

	r := &recorder{}
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "quit", "quit", "q", r.handler("quit"))

	require.True(t, d.HasAction("quit"), "SetAction must record the action under its name")

	consumed, _ := d.Dispatch("q")
	require.True(t, consumed, "SetAction must also bind the key like Set")
	require.Equal(t, "quit", r.lastLabel())
}

func TestApplyOverrides_MultipleKeysPerAction(t *testing.T) {
	t.Parallel()

	r := &recorder{}
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "refresh", "refresh", "r", r.handler("refresh"))

	require.NoError(t, d.ApplyOverrides(map[string][]string{
		"refresh": {"R", "F5"},
	}))

	for _, key := range []string{"r", "R", "F5"} {
		consumed, _ := d.Dispatch(key)
		require.True(t, consumed, "key %q must fire after override", key)
	}
	require.EqualValues(t, 3, r.count.Load())
}

func TestApplyOverrides_UnknownActionFailsClosed(t *testing.T) {
	t.Parallel()

	// User typo'd `quitt` — better to refuse to start than to
	// silently drop the binding and leave the user wondering why
	// their keybinding does nothing.
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "quit", "quit", "q", func() tea.Cmd { return nil })

	err := d.ApplyOverrides(map[string][]string{
		"quitt": {"Q"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown action "quitt"`)
}

func TestApplyOverrides_PreservesOriginalLayerAcrossUserKey(t *testing.T) {
	t.Parallel()

	// User's extra key must inherit the action's layer so an action
	// registered at LayerGlobal stays a global affordance and an
	// action at LayerView stays scoped to that view. Without this
	// the override could "promote" a view-local binding into a
	// global one and override another action's binding in surprising
	// ways.
	r := &recorder{}
	d := New(newFakeClock())
	d.SetAction(LayerView, "yank-id", "yank id", "y", r.handler("yank"))

	require.NoError(t, d.ApplyOverrides(map[string][]string{
		"yank-id": {"Y"},
	}))

	// Now register a different handler for "Y" at LayerGlobal —
	// LayerView (where "yank-id" lives) takes precedence over global
	// per the dispatcher's layer order, so the user binding wins.
	conflicting := r.handler("global")
	d.Set(LayerGlobal, "Y", conflicting)

	consumed, _ := d.Dispatch("Y")
	require.True(t, consumed)
	require.Equal(t, "yank", r.lastLabel(),
		"override Y must inherit yank-id's view layer and beat the global Y")
}

func TestDispatcher_ClearRemovesAllLayerBindings(t *testing.T) {
	t.Parallel()

	// A modal that registered three keys in LayerModal during Open
	// must be able to wipe all three in one shot during Close. Per-key
	// Unregister would force the modal to remember every key it
	// registered; Clear(layer) is the natural shape — "drop every
	// binding I owned in this layer".
	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerModal, "y", r.handler("yes"))
	d.Set(LayerModal, "n", r.handler("no"))
	d.Set(LayerModal, "Esc", r.handler("cancel"))

	d.Clear(LayerModal)

	for _, key := range []string{"y", "n", "Esc"} {
		consumed, _ := d.Dispatch(key)
		require.False(t, consumed,
			"key %q must miss after Clear(LayerModal) — the layer is empty", key)
	}
	require.EqualValues(t, 0, r.count.Load(),
		"no modal handler may have fired after Clear")
}

func TestDispatcher_ClearRestoresUnderlyingLayer(t *testing.T) {
	t.Parallel()

	// Load-bearing scenario: a modal registers `q` to close itself
	// while the global `q` quits the app. While the modal is open,
	// modal `q` wins (LayerModal beats LayerGlobal). After the modal
	// closes and Clear(LayerModal) fires, the global `q` must take
	// over again — without Clear, the modal's `q` would linger
	// forever and shadow the global handler.
	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerGlobal, "q", r.handler("global-quit"))
	d.Set(LayerModal, "q", r.handler("modal-close"))

	// While modal is "open", its `q` wins.
	consumed, _ := d.Dispatch("q")
	require.True(t, consumed)
	require.Equal(t, "modal-close", r.lastLabel(),
		"modal layer must shadow global while populated")

	// Modal closes → Clear wipes the layer.
	d.Clear(LayerModal)

	consumed, _ = d.Dispatch("q")
	require.True(t, consumed)
	require.Equal(t, "global-quit", r.lastLabel(),
		"after Clear(LayerModal), global q must win again")
}

func TestDispatcher_ClearDropsActionEntriesInLayer(t *testing.T) {
	t.Parallel()

	// Clear must also scrub the action registry for the cleared
	// layer, otherwise ApplyOverrides would still wire user-extra
	// keys onto a handler whose layer has just been wiped — the
	// override would silently re-populate the cleared layer with a
	// stale handler.
	r := &recorder{}
	d := New(newFakeClock())
	d.SetAction(LayerModal, "modal-confirm", "confirm", "y", r.handler("yes"))
	d.SetAction(LayerGlobal, "quit", "quit", "q", r.handler("quit"))

	d.Clear(LayerModal)

	require.False(t, d.HasAction("modal-confirm"),
		"action belonging to cleared layer must be gone")
	require.True(t, d.HasAction("quit"),
		"actions in untouched layers must survive")
}

func TestSet_OverwritesSilently(t *testing.T) {
	t.Parallel()

	// Last write wins for anonymous bindings; the dispatcher does
	// not enforce duplicate detection — callers validate.
	r := &recorder{}
	d := New(newFakeClock())
	d.Set(LayerGlobal, "x", r.handler("first"))
	d.Set(LayerGlobal, "x", r.handler("second"))

	_, _ = d.Dispatch("x")
	require.Equal(t, "second", r.lastLabel())
}

func TestBindings_ReturnsRegistrationOrder(t *testing.T) {
	t.Parallel()

	d := New(newFakeClock())
	noop := func() tea.Cmd { return nil }
	d.SetAction(LayerGlobal, "command", "command", ":", noop)
	d.SetAction(LayerGlobal, "filter", "filter", "/", noop)
	d.SetAction(LayerGlobal, "help", "help", "?", noop)

	got := d.Bindings(LayerGlobal)
	require.Equal(t, []action.Action{
		{Key: ":", Description: "command"},
		{Key: "/", Description: "filter"},
		{Key: "?", Description: "help"},
	}, got, "Bindings must follow registration order so the help "+
		"overlay's GENERAL column stays muscle-memory-stable")
}

func TestBindings_FiltersByLayer(t *testing.T) {
	t.Parallel()

	d := New(newFakeClock())
	noop := func() tea.Cmd { return nil }
	d.SetAction(LayerGlobal, "quit", "quit", "q", noop)
	d.SetAction(LayerView, "silence", "silence alert", "s", noop)
	d.SetAction(LayerGlobal, "help", "help", "?", noop)

	globals := d.Bindings(LayerGlobal)
	require.Equal(t, []action.Action{
		{Key: "q", Description: "quit"},
		{Key: "?", Description: "help"},
	}, globals)

	view := d.Bindings(LayerView)
	require.Equal(t, []action.Action{
		{Key: "s", Description: "silence alert"},
	}, view)
}

func TestBindings_EmptyLayerReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "quit", "quit", "q", func() tea.Cmd { return nil })

	require.Empty(t, d.Bindings(LayerView),
		"layer with no named registrations must yield an empty slice")
	require.Empty(t, d.Bindings(LayerTable))
}

func TestBindings_ExcludesAnonymousSetBindings(t *testing.T) {
	t.Parallel()

	// `gg` (chord) and `Ctrl+\` (clear marks) are registered via the
	// raw Set seam — no name, no description. The help overlay must
	// not see them; they live in keybindings.md under their own
	// sections, not the GENERAL column.
	d := New(newFakeClock())
	noop := func() tea.Cmd { return nil }
	d.Set(LayerTable, "gg", noop)
	d.Set(LayerGlobal, "Ctrl+\\", noop)
	d.SetAction(LayerGlobal, "quit", "quit", "q", noop)

	require.Equal(t, []action.Action{
		{Key: "q", Description: "quit"},
	}, d.Bindings(LayerGlobal), "anonymous Set bindings must not surface in Bindings")
	require.Empty(t, d.Bindings(LayerTable))
}

func TestBindings_DescriptionTravelsVerbatim(t *testing.T) {
	t.Parallel()

	// Descriptions are pass-through — the dispatcher does not derive
	// them from the name (no kebab-to-space transform, no munging).
	// Whatever the caller hands SetAction lands in Bindings() exactly.
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "tenant-picker", "tenant picker", "Ctrl+T", func() tea.Cmd { return nil })

	got := d.Bindings(LayerGlobal)
	require.Len(t, got, 1)
	require.Equal(t, "tenant picker", got[0].Description)
}

func TestSetActionDisplayKey_OverridesChipText(t *testing.T) {
	t.Parallel()

	// `:` triggers command mode but the help chip reads `:cmd` so the
	// operator sees "type colon, then a command name" (ADR 0038).
	// SetActionDisplayKey wires DisplayKey on the existing action;
	// the trigger key stays unchanged so dispatching `:` still fires.
	noop := func() tea.Cmd { return nil }
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "command", "Command mode", ":", noop)
	d.SetActionDisplayKey("command", ":cmd")

	got := d.Bindings(LayerGlobal)
	require.Equal(t,
		[]action.Action{{Key: ":", DisplayKey: ":cmd", Description: "Command mode"}},
		got,
		"Bindings() must carry the DisplayKey override to the help overlay")
	consumed, _ := d.Dispatch(":")
	require.True(t, consumed, "the trigger key (`:`) must still dispatch — only the chip label changed")
}

func TestSetActionDisplayKey_UnknownActionPanics(t *testing.T) {
	t.Parallel()

	// Wiring a display override against an action the caller forgot
	// to register is a programmer error (same severity as Register
	// panics on empty alias) — fail fast at the seam.
	require.Panics(t, func() {
		d := New(newFakeClock())
		d.SetActionDisplayKey("nope", ":nope")
	})
}

func TestSetActionDisplayKey_EmptyOverrideClearsField(t *testing.T) {
	t.Parallel()

	// An explicit empty argument is the supported way to walk back an
	// override without re-running SetAction. The chip falls back to
	// Key on the next Bindings() snapshot.
	noop := func() tea.Cmd { return nil }
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "command", "Command mode", ":", noop)
	d.SetActionDisplayKey("command", ":cmd")
	d.SetActionDisplayKey("command", "")

	got := d.Bindings(LayerGlobal)
	require.Empty(t, got[0].DisplayKey,
		"empty argument must clear the override so the chip falls back to Key")
}

func TestSetAction_ReRegisterClearsDisplayKey(t *testing.T) {
	t.Parallel()

	// SetAction is last-write-wins across every field, including
	// displayKey. A caller that re-registers the same action after
	// applying a chip override gets a fresh entry — the override does
	// NOT survive. Callers that want a persistent override must
	// re-apply SetActionDisplayKey after the second SetAction.
	noop := func() tea.Cmd { return nil }
	d := New(newFakeClock())
	d.SetAction(LayerGlobal, "command", "Command mode", ":", noop)
	d.SetActionDisplayKey("command", ":cmd")
	d.SetAction(LayerGlobal, "command", "command", ":", noop)

	got := d.Bindings(LayerGlobal)
	require.Empty(t, got[0].DisplayKey,
		"re-registration must clear any prior SetActionDisplayKey override")
}

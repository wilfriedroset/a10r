// SPDX-License-Identifier: Apache-2.0

// Package keys is the J2 / keybindings.md dispatcher: incoming key
// events flow through five precedence layers (modal > prompt >
// per-view > table-context > global) and the first match wins.
// Multi-key chords are supported with a 500 ms timeout — v0.1 only
// uses one chord (`gg` for "first row" per the table-context
// catalog) but the mechanism is general.
//
// The dispatcher is deliberately decoupled from UI code: it owns
// the key-to-handler map and the chord buffer, but knows nothing
// about prompts, pages, or rendering. The app shell (#22) wires
// tea.KeyMsg → string here, runs Dispatch, and applies the
// resulting tea.Cmd. Tests inject string keys directly without
// going through bubbletea.
package keys

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
)

// ChordTimeout is the window within which a chord's keys must
// arrive. Per keybindings.md §Conventions: 500 ms.
const ChordTimeout = 500 * time.Millisecond

// Layer enumerates the five precedence levels in keybindings.md
// §Precedence order: highest first. Dispatch walks the layers in
// declaration order so a later registration in the same layer
// shadows the earlier one — but a higher-precedence layer always
// beats every layer below it, regardless of registration order.
type Layer int

const (
	// LayerModal — confirm dialogs, picker overlays, help overlay.
	LayerModal Layer = iota
	// LayerPrompt — open `:` command bar / `/` filter.
	LayerPrompt
	// LayerView — bindings on the active page.
	LayerView
	// LayerTable — bindings shared across every table-bodied page.
	LayerTable
	// LayerGlobal — always-available bindings (`?`, `q`, `Ctrl+C`, …).
	LayerGlobal

	numLayers = int(LayerGlobal) + 1
)

// Handler is the runtime callback for one keybinding. Returning a
// nil tea.Cmd is fine — many bindings just toggle local state and
// rely on the next render to show the change.
type Handler func() tea.Cmd

// KeyMap is one layer's binding map. Keys are the wire-level
// strings from keybindings.md (`s`, `Ctrl+S`, `Shift+E`, `gg`).
type KeyMap map[string]Handler

// chordPrefix is the first key of every supported chord. v0.1 has
// only `gg` so this is `{"g": "gg"}`. Generalised so a future chord
// like `bs` (bulk-silence) can be added by extending this map and
// the precedence stack picks it up automatically.
var chordPrefix = map[string]string{
	"g": "gg",
}

// Dispatcher routes key events through the precedence stack with
// chord-buffer support. Construct via New.
//
// Not safe for concurrent use. The dispatcher mutates internal
// chord state on every Dispatch / HandleChordExpired call and is
// intended for the single-goroutine bubbletea Update loop. Callers
// that need fan-out from multiple goroutines must wrap with their
// own synchronisation.
type Dispatcher struct {
	layers [numLayers]KeyMap

	// actions indexes registered handlers by stable action name so
	// ApplyOverrides can wire user-supplied keys onto the matching
	// (layer, handler) pair. Populated only via SetAction; SetWithout-
	// Action bindings (chords like `gg`, dispatcher-internal hooks)
	// stay invisible to overrides because the v0.0.1 schema only lets
	// users bind to named actions registered through SetAction.
	actions map[string]actionEntry

	// Chord state. chordPending is the prefix key the user has
	// already pressed (e.g. "g") and chordExpiry is when the
	// timeout fires. Zero value of chordPending means no chord is
	// in flight.
	chordPending string
	chordExpiry  time.Time

	clock Clock
}

// actionEntry is the (layer, handler) pair the action registry
// records per stable action name. ApplyOverrides walks this map to
// find each user-extra key's destination.
type actionEntry struct {
	layer   Layer
	handler Handler
}

// ChordExpiredMsg is the message tea.Tick fires when the chord
// timeout elapses. The app shell routes this to HandleChordExpired
// after the dispatcher's Dispatch returns the corresponding
// scheduling Cmd.
type ChordExpiredMsg struct {
	// At is the time tea.Tick scheduled the message for. The
	// dispatcher uses it to discard stale ticks: a key arrived
	// after the original chord but before the original tick fired,
	// so this message is for an already-resolved chord.
	At time.Time
}

// New constructs a Dispatcher with an empty key map at every layer.
// Callers register bindings via Set. nil clock defaults to
// SystemClock so the production wiring is a one-line New().
func New(clock Clock) *Dispatcher {
	if clock == nil {
		clock = SystemClock{}
	}
	d := &Dispatcher{
		clock:   clock,
		actions: map[string]actionEntry{},
	}
	for i := range d.layers {
		d.layers[i] = KeyMap{}
	}
	return d
}

// Set registers a single binding at the given layer. Re-registering
// the same key at the same layer overwrites silently — the action
// registry in the action package owns duplicate-detection; this
// dispatcher trusts the caller to have validated.
//
// Bindings registered via Set are NOT exposed to user overrides —
// use SetAction for any binding that should be user-extensible
// through `<config-dir>/keys/<profile>.yaml`. Set is reserved for
// chord prefixes, dispatcher-internal hooks, and tenant quick-switch
// digits which are deliberately non-overridable.
func (d *Dispatcher) Set(layer Layer, key string, h Handler) {
	d.layers[layer][key] = h
}

// SetAction registers a binding under a stable action name AND
// installs it at the given (layer, key). The action name is the
// identifier users reference in `<config-dir>/keys/<profile>.yaml`
// to add extra bindings; the layer + handler captured here are the
// destination ApplyOverrides wires those extra keys into.
//
// Re-registering the same action overwrites the recorded entry
// (last write wins, matching Set's semantics) so deferred wiring
// can re-bind without a discrete clear step.
func (d *Dispatcher) SetAction(layer Layer, action, key string, h Handler) {
	d.actions[action] = actionEntry{layer: layer, handler: h}
	d.Set(layer, key, h)
}

// ApplyOverrides binds every user-supplied extra key onto the
// matching action's (layer, handler) pair.
//
// "Shadow defaults" semantics (per ADR 0010): user keys are
// ADDITIONAL bindings — the original key registered via SetAction
// continues to work alongside any user extras. Conflicts WITHIN the
// user file are caught by the loader (config.LoadKeys) with file:line
// precision; this method's only error path is "user named an action
// that was never registered".
//
// Idempotent: calling twice with the same overrides is a no-op
// because Set's last-write-wins semantics replace identical handlers
// with themselves.
func (d *Dispatcher) ApplyOverrides(overrides map[string][]string) error {
	// Sort actions for a deterministic error path — without this,
	// two unknown actions would surface in map-iteration order, which
	// flaps across runs and across the test suite.
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := d.actions[name]
		if !ok {
			return fmt.Errorf("unknown action %q (no built-in binding registered under that name)", name)
		}
		for _, key := range overrides[name] {
			d.Set(entry.layer, key, entry.handler)
		}
	}
	return nil
}

// HasAction reports whether an action is registered. Exposed so
// diagnostics callers (validate / doctor) can pre-flight a user
// keybindings file without invoking ApplyOverrides — `a10r validate`
// is the natural fit when it grows a keys check, but the predicate
// is small enough to ship now without paying for it later.
func (d *Dispatcher) HasAction(name string) bool {
	_, ok := d.actions[name]
	return ok
}

// Dispatch routes one key event through the precedence stack.
// Returns (consumed, cmd):
//
//   - consumed=true means the key was claimed by the dispatcher;
//     the caller should not propagate it further.
//   - cmd is whatever tea.Cmd the matched handler returned, or a
//     scheduling Cmd that fires a ChordExpiredMsg when the chord
//     window elapses, or nil.
//
// Lazy chord resolution: a pending chord is resolved on the next
// Dispatch call, comparing the elapsed time against ChordTimeout.
// HandleChordExpired exists for the case where no key arrives
// within the window — the app shell schedules a tea.Tick when
// Dispatch consumes a chord prefix and routes the resulting
// ChordExpiredMsg back here.
func (d *Dispatcher) Dispatch(key string) (consumed bool, cmd tea.Cmd) {
	now := d.clock.Now()

	if d.chordPending != "" {
		// Someone is mid-chord. Resolve based on the new key.
		pending := d.chordPending
		d.chordPending = ""

		if !now.Before(d.chordExpiry) {
			// Chord expired before this key arrived. Discard and
			// dispatch the new key fresh.
			return d.dispatchFresh(key, now)
		}

		// Within the window: try to complete the chord.
		combined := pending + key
		if h, ok := d.lookup(combined); ok {
			return true, h()
		}
		// Combined isn't a known chord — fall through and dispatch
		// the new key as a normal single-key event.
	}

	return d.dispatchFresh(key, now)
}

// dispatchFresh handles a key that is NOT a chord-completion. If
// the key is a chord prefix it starts a new chord and schedules
// the timeout; otherwise it walks the layer stack.
func (d *Dispatcher) dispatchFresh(key string, now time.Time) (bool, tea.Cmd) {
	if chord, ok := chordPrefix[key]; ok && d.hasBinding(chord) {
		d.chordPending = key
		d.chordExpiry = now.Add(ChordTimeout)
		return true, tea.Tick(ChordTimeout, func(t time.Time) tea.Msg {
			return ChordExpiredMsg{At: t}
		})
	}
	if h, ok := d.lookup(key); ok {
		return true, h()
	}
	return false, nil
}

// HandleChordExpired processes a ChordExpiredMsg. Idempotent:
// stale ticks (the chord was resolved by a key arrival before the
// tick fired) are discarded silently. No tea.Cmd is currently
// fired since v0.1's only chord prefix (`g`) has no single-key
// binding to fall back to; the contract is preserved so a future
// single-key fallback can land additively.
func (d *Dispatcher) HandleChordExpired(msg ChordExpiredMsg) tea.Cmd {
	if d.chordPending == "" {
		return nil
	}
	if msg.At.Before(d.chordExpiry) {
		// A newer chord supersedes this tick.
		return nil
	}
	d.chordPending = ""
	return nil
}

// lookup walks the layer stack in precedence order and returns the
// first matching handler.
func (d *Dispatcher) lookup(key string) (Handler, bool) {
	for i := range d.layers {
		if h, ok := d.layers[i][key]; ok {
			return h, true
		}
	}
	return nil, false
}

// hasBinding reports whether any layer has a handler for key.
func (d *Dispatcher) hasBinding(key string) bool {
	_, ok := d.lookup(key)
	return ok
}

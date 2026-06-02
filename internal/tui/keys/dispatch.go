// SPDX-License-Identifier: Apache-2.0

// Package keys is the keybindings dispatcher: key events flow through
// five precedence layers (modal > prompt > per-view > table-context >
// global) and the first match wins. Multi-key chords are supported
// with a 500 ms timeout. It owns the key-to-handler map and chord
// buffer but knows nothing about prompts, pages, or rendering.
package keys

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/clock"
	"github.com/wilfriedroset/a10r/internal/tui/action"
)

// ChordTimeout is the window within which a chord's keys must arrive,
// per keybindings.md §Conventions.
const ChordTimeout = 500 * time.Millisecond

// Layer enumerates the five precedence levels (keybindings.md
// §Precedence), highest first. A higher-precedence layer always beats
// every layer below it regardless of registration order.
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

// Handler is the runtime callback for one keybinding. A nil tea.Cmd
// return is valid: many bindings only toggle local state.
type Handler func() tea.Cmd

// KeyMap is one layer's binding map. Keys are the wire-level
// strings from keybindings.md (`s`, `Ctrl+S`, `Shift+E`, `gg`).
type KeyMap map[string]Handler

// chordPrefix maps the first key of every supported chord to the full
// chord; the precedence stack picks up any entry added here.
var chordPrefix = map[string]string{
	"g": "gg",
}

// Dispatcher routes key events through the precedence stack with
// chord-buffer support. Construct via New. Not safe for concurrent
// use: it mutates chord state on every call and targets the
// single-goroutine bubbletea Update loop.
type Dispatcher struct {
	layers [numLayers]KeyMap

	// actions indexes handlers by stable action name so ApplyOverrides
	// can wire user keys onto the matching (layer, handler) pair.
	// Populated only via SetAction; anonymous Set bindings stay
	// invisible to overrides.
	actions map[string]actionEntry

	// actionOrder records action names in registration order. Go map
	// iteration is non-deterministic; this slice is what makes
	// Bindings(layer) return a stable list — load-bearing for the help
	// overlay's GENERAL column ordering. Anonymous Set bindings carry
	// no name and are intentionally absent.
	actionOrder []string

	// chordPending is the prefix key already pressed; empty means no
	// chord is in flight. chordExpiry is when the timeout fires.
	chordPending string
	chordExpiry  time.Time

	clock clock.Now
}

// actionEntry is the tuple recorded per stable action name.
// ApplyOverrides reads (layer, handler) to wire user extras; Bindings
// reads (key, displayKey, description) for the help overlay.
type actionEntry struct {
	layer       Layer
	key         string
	displayKey  string
	description string
	handler     Handler
}

// ChordExpiredMsg is the message tea.Tick fires when the chord timeout
// elapses; the app shell routes it to HandleChordExpired.
type ChordExpiredMsg struct {
	// At is the time tea.Tick scheduled the message for, used to
	// discard stale ticks whose chord was already resolved by a key.
	At time.Time
}

// New constructs a Dispatcher with an empty key map at every layer.
// nil c defaults to clock.System.
func New(c clock.Now) *Dispatcher {
	if c == nil {
		c = clock.System{}
	}
	d := &Dispatcher{
		clock:   c,
		actions: map[string]actionEntry{},
	}
	for i := range d.layers {
		d.layers[i] = KeyMap{}
	}
	return d
}

// Set registers a single binding at the given layer, overwriting any
// existing key silently. Bindings registered via Set are NOT exposed
// to user overrides — use SetAction for user-extensible bindings. Set
// is reserved for chord prefixes, dispatcher-internal hooks, and
// tenant quick-switch digits, which are deliberately non-overridable.
func (d *Dispatcher) Set(layer Layer, key string, h Handler) {
	d.layers[layer][key] = h
}

// SetAction registers a binding under a stable action name and
// installs it at (layer, key). The name is what users reference in
// `<config-dir>/keys/<profile>.yaml`; description is the help-overlay
// label.
//
// Re-registering the same name keeps its actionOrder slot so Bindings
// output stays stable; every other field is last-write-wins, so a
// prior SetActionDisplayKey override must be re-applied afterward.
func (d *Dispatcher) SetAction(layer Layer, name, description, key string, h Handler) {
	if _, exists := d.actions[name]; !exists {
		d.actionOrder = append(d.actionOrder, name)
	}
	d.actions[name] = actionEntry{layer: layer, key: key, description: description, handler: h}
	d.Set(layer, key, h)
}

// Bindings returns the named actions registered at layer, in
// registration order, for the help overlay. Anonymous Set bindings
// are excluded — they carry no description and are not user-visible.
func (d *Dispatcher) Bindings(layer Layer) []action.Action {
	out := make([]action.Action, 0, len(d.actionOrder))
	for _, name := range d.actionOrder {
		entry, ok := d.actions[name]
		if !ok || entry.layer != layer {
			continue
		}
		out = append(out, action.Action{
			Key:         entry.key,
			DisplayKey:  entry.displayKey,
			Description: entry.description,
		})
	}
	return out
}

// SetActionDisplayKey overrides the chip label painted for an action
// without changing its trigger key; empty string clears the override
// (ADR 0038). Panics on an unregistered action, matching the package's
// programmer-error convention so a wiring typo surfaces immediately.
func (d *Dispatcher) SetActionDisplayKey(name, displayKey string) {
	entry, ok := d.actions[name]
	if !ok {
		panic("keys: SetActionDisplayKey on unregistered action: " + name)
	}
	entry.displayKey = displayKey
	d.actions[name] = entry
}

// Clear wipes every binding in the named layer and drops the matching
// action-registry entries. The natural caller is a modal's Close path:
// without Clear, its bindings would linger and keep shadowing
// lower-precedence layers. Scrubbing the action registry is part of
// the contract so a later ApplyOverrides can't find a stale action
// whose handler points into a wiped layer.
func (d *Dispatcher) Clear(layer Layer) {
	d.layers[layer] = KeyMap{}
	kept := d.actionOrder[:0]
	for _, name := range d.actionOrder {
		entry, ok := d.actions[name]
		if !ok {
			continue
		}
		if entry.layer == layer {
			delete(d.actions, name)
			continue
		}
		kept = append(kept, name)
	}
	d.actionOrder = kept
}

// ApplyOverrides binds every user-supplied extra key onto the matching
// action's (layer, handler) pair. "Shadow defaults" semantics (ADR
// 0010): user keys are additional, the original SetAction key still
// works. The only error path is an unknown action name. Idempotent.
func (d *Dispatcher) ApplyOverrides(overrides map[string][]string) error {
	// Sort for a deterministic error path: unknown actions would
	// otherwise surface in flapping map-iteration order.
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
// diagnostics (validate / doctor) can pre-flight a user keybindings
// file without invoking ApplyOverrides.
func (d *Dispatcher) HasAction(name string) bool {
	_, ok := d.actions[name]
	return ok
}

// Dispatch routes one key event through the precedence stack. Returns
// (consumed, cmd): consumed=true means the dispatcher claimed the key
// and the caller must not propagate it; cmd is the matched handler's
// tea.Cmd, a scheduling Cmd for the chord timeout, or nil.
//
// A pending chord is resolved lazily on the next Dispatch by comparing
// elapsed time against ChordTimeout; HandleChordExpired covers the case
// where no key arrives within the window.
func (d *Dispatcher) Dispatch(key string) (consumed bool, cmd tea.Cmd) {
	now := d.clock.Now()

	if d.chordPending != "" {
		pending := d.chordPending
		d.chordPending = ""

		if !now.Before(d.chordExpiry) {
			return d.dispatchFresh(key, now)
		}

		combined := pending + key
		if h, ok := d.lookup(combined); ok {
			return true, h()
		}
	}

	return d.dispatchFresh(key, now)
}

// dispatchFresh handles a key that is not a chord completion: a chord
// prefix starts a new chord and schedules the timeout; anything else
// walks the layer stack.
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

// HandleChordExpired processes a ChordExpiredMsg. Idempotent: stale
// ticks (chord already resolved by a key arrival) are discarded
// silently. The only chord prefix has no single-key fallback, so this
// returns nil.
func (d *Dispatcher) HandleChordExpired(msg ChordExpiredMsg) tea.Cmd {
	if d.chordPending == "" {
		return nil
	}
	if msg.At.Before(d.chordExpiry) {
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

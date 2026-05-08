// SPDX-License-Identifier: Apache-2.0

// Package action holds the J2 keybinding registry — the metadata
// store that drives the J1 hint strip, the help overlay, and the
// C4 read-only filter. Actions are pure data here; the runtime
// dispatcher (#19) wires keys to handlers and the per-page bindings
// (#27 onwards) populate the registry.
//
// Splitting metadata (this package) from dispatch (the keys package
// next door) keeps the registry trivially testable and gives every
// view the same shape: a slice of Action records that can be
// rendered, filtered, or queried without spinning up a tea.Program.
package action

import (
	"errors"
	"fmt"
)

// ErrDuplicate is the sentinel wrapped in the Register panic value
// when a (View, Key) pair is already bound. Tests assert via
// errors.Is rather than literal-string match so a future tweak to
// the panic message format does not break the contract.
var ErrDuplicate = errors.New("duplicate action registration")

// Action is one keybinding's metadata. Run is intentionally NOT
// part of this struct — coupling actions to tea.Cmd here would pull
// bubbletea into the registry, which the J2 / J1 design wants to
// keep separate. The dispatcher in keys/ holds the (View, Key) →
// handler map, looking up the runtime callback by Action.Key when
// a key arrives.
type Action struct {
	// Key is the wire-level binding string from keybindings.md
	// (e.g. "s", "Ctrl+S", "Shift+E", "gg" for the chord).
	Key string

	// Description is shown in the J1 hint strip and the help
	// overlay. Short imperative phrases ("silence alert", "expire
	// silence", "refresh") read best in both contexts.
	Description string

	// View is the per-view scope this action belongs to. Empty
	// means "global" — visible across every view. Concrete view
	// names match the page packages (alerts, silences, status,
	// receivers, groups, alert, tenant, silence_form).
	View string

	// Dangerous flags actions filtered out when read-only mode is
	// active (per C4): silence create / update / expire, Mimir
	// config writes (post-v0.1), and any other state-mutating verb.
	Dangerous bool

	// Bulk flags actions that require prior Space-mark selection.
	// The dispatcher surfaces a flash hint when a Bulk action
	// fires with no rows marked rather than silently no-oping.
	Bulk bool
}

// registryKey is the (View, Key) tuple Registry uses to detect
// duplicate registrations. The same Key in different Views is
// allowed — `s` for silence-from-alert is registered separately on
// the alerts and the alert-detail views.
type registryKey struct {
	View string
	Key  string
}

// Registry holds every Action a TUI session will know about. Pages
// register their bindings during construction; the dispatcher and
// the help overlay walk the registry by view at render time.
//
// The zero value is NOT usable — call New() so the duplicate-detect
// map is initialised.
type Registry struct {
	actions []Action
	seen    map[registryKey]struct{}
}

// New constructs an empty Registry.
func New() *Registry {
	return &Registry{seen: map[registryKey]struct{}{}}
}

// Register adds an Action. Panics on a duplicate (View, Key) pair —
// double-registering a binding is a programmer error caught at
// startup rather than letting the second registration silently
// shadow the first. Production code registers bindings once during
// page construction; tests and the help overlay never need a
// dynamic Register/Unregister cycle.
//
// The panic value wraps ErrDuplicate so tests can assert via
// errors.Is(recovered, ErrDuplicate) without coupling to the exact
// message format.
func (r *Registry) Register(a Action) {
	k := registryKey{View: a.View, Key: a.Key}
	if _, dup := r.seen[k]; dup {
		panic(fmt.Errorf("%w: view=%q key=%q", ErrDuplicate, a.View, a.Key))
	}
	r.seen[k] = struct{}{}
	r.actions = append(r.actions, a)
}

// Hints returns the actions visible for the given view, plus any
// global (View=="") actions, in registration order. The J1 header
// hint strip and the help overlay both render this slice.
//
// Returned slice is a fresh copy: mutating it does not affect the
// registry. Symmetric with All() and Filter().
func (r *Registry) Hints(view string) []Action {
	out := make([]Action, 0, len(r.actions))
	for _, a := range r.actions {
		if a.View == "" || a.View == view {
			out = append(out, a)
		}
	}
	return out
}

// Filter applies the C4 read-only mask. When readOnly is true,
// every Action whose Dangerous flag is set is omitted. When false
// the full registry is returned.
//
// Filter is independent of Hints because read-only mode applies to
// every view at once — the per-view scoping happens via Hints.
// Callers compose them: `r.Filter(readOnly)` then per-view filter,
// or `r.Hints(view)` then per-call read-only filter, depending on
// whether they need the global cross-view list or a single view's.
//
// Returned slice is a fresh copy: mutating it does not affect the
// registry. Symmetric with All() and Hints().
func (r *Registry) Filter(readOnly bool) []Action {
	if !readOnly {
		return r.All()
	}
	out := make([]Action, 0, len(r.actions))
	for _, a := range r.actions {
		if !a.Dangerous {
			out = append(out, a)
		}
	}
	return out
}

// All returns a copy of every registered action, in registration
// order. Returned slice is safe for the caller to mutate without
// affecting the Registry.
func (r *Registry) All() []Action {
	out := make([]Action, len(r.actions))
	copy(out, r.actions)
	return out
}

// Len returns the number of registered actions.
func (r *Registry) Len() int { return len(r.actions) }

// FilterDangerous returns a fresh slice containing every entry of
// in whose Dangerous flag is unset. Pages call it on their
// Bindings() output when read-only mode is active so the hint
// strip and the help overlay drop the write verbs without each
// consumer re-implementing the predicate.
//
// Returns the input slice's elements when none are Dangerous —
// safe because callers treat the output as read-only.
func FilterDangerous(in []Action) []Action {
	out := make([]Action, 0, len(in))
	for _, a := range in {
		if !a.Dangerous {
			out = append(out, a)
		}
	}
	return out
}

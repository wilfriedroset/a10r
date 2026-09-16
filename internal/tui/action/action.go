// SPDX-License-Identifier: Apache-2.0

// Package action holds the shared keybinding metadata type pages emit
// from Bindings() and the read-only filter they apply to it. Living in
// a leaf package keeps it free of bubbletea or any UI dependency (per
// ADR 0019).
package action

// Action is one keybinding's metadata. Run is intentionally NOT part of
// this struct: coupling actions to tea.Cmd would pull bubbletea into a
// leaf package. The dispatcher in keys/ owns the (layer, key) → handler
// map; this type carries only rendering metadata.
type Action struct {
	// Key is the wire-level binding string from keybindings.md the
	// dispatcher matches against incoming key events (e.g. "s",
	// "Ctrl+S", "gg" for the chord).
	Key string

	// DisplayKey overrides the chip label when set; empty falls back to
	// Key. For bindings whose affordance label disagrees with the
	// dispatched key (e.g. `:` rendering as `:cmd`). Read via ChipKey so
	// the precedence stays in one place.
	DisplayKey string

	// Description is the short imperative phrase shown in the header hint
	// strip and the help overlay.
	Description string

	// View is the per-view scope; empty means global. Names match the
	// page packages (alerts, silences, status, ...).
	View string

	// Dangerous flags state-mutating verbs filtered out in read-only mode
	// (silence create / update / expire, future Mimir config writes).
	Dangerous bool

	// Guarded flags a verb a `guardrails:` rule denies on the tenant
	// the page currently points at. Unlike Dangerous it is per tenant
	// and per render, so the verb stays in Bindings() and the help
	// overlay keeps showing it with a [guarded] suffix; only the hint
	// strip drops it, the way read-only drops a Dangerous verb.
	Guarded bool

	// Bulk flags actions requiring prior Space-mark selection. The
	// dispatcher flashes a hint when one fires with no rows marked rather
	// than silently no-oping.
	Bulk bool

	// Shared flags a cross-cutting binding (the table-wide Space/mark verb
	// every list page reuses). Mirrors k9s's KeyAction.Shared: the help
	// overlay folds these into GENERAL so the verb reads once, but the
	// footer hint strip still lists it per page.
	Shared bool
}

// ChipKey is the precedence point for chip rendering: DisplayKey when
// set, Key otherwise.
func (a Action) ChipKey() string {
	if a.DisplayKey != "" {
		return a.DisplayKey
	}
	return a.Key
}

// FilterGuarded returns a fresh slice of the entries no guardrail rule
// denies, so the hint strip stops advertising a key that would only
// flash a refusal.
func FilterGuarded(in []Action) []Action {
	out := make([]Action, 0, len(in))
	for _, a := range in {
		if !a.Guarded {
			out = append(out, a)
		}
	}
	return out
}

// FilterDangerous returns a fresh slice of the entries whose Dangerous
// flag is unset, so read-only mode drops write verbs without every
// consumer re-implementing the predicate.
func FilterDangerous(in []Action) []Action {
	out := make([]Action, 0, len(in))
	for _, a := range in {
		if !a.Dangerous {
			out = append(out, a)
		}
	}
	return out
}

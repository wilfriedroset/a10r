// SPDX-License-Identifier: Apache-2.0

// Package action holds the shared data type every page's Bindings()
// returns and the read-only filter every page applies to its own
// slice. There is no registry: pages emit []Action directly from
// their Bindings() methods, and the help overlay's GENERAL column
// is derived from the dispatcher (per ADR 0019) rather than from a
// separate metadata store.
//
// Keeping Action and FilterDangerous in a leaf package lets every
// page, the help overlay, and the dispatcher share the same shape
// without pulling bubbletea or any UI dependency in.
package action

// Action is one keybinding's metadata. Run is intentionally NOT
// part of this struct — coupling actions to tea.Cmd here would pull
// bubbletea into a leaf package. The dispatcher in keys/ holds the
// (layer, key) → handler map; this type carries the rendering
// metadata the help overlay and the header hint strip read.
type Action struct {
	// Key is the wire-level binding string from keybindings.md
	// (e.g. "s", "Ctrl+S", "Shift+E", "gg" for the chord).
	Key string

	// Description is shown in the header hint strip and the help
	// overlay. Short imperative phrases ("silence alert", "expire
	// silence", "refresh") read best in both contexts.
	Description string

	// View is the per-view scope this action belongs to. Empty
	// means "global" — visible across every view. Concrete view
	// names match the page packages (alerts, silences, status,
	// receivers, groups, alert, tenant, silence_form).
	View string

	// Dangerous flags actions filtered out when read-only mode is
	// active: silence create / update / expire, Mimir config writes
	// (post-v0.1), and any other state-mutating verb.
	Dangerous bool

	// Bulk flags actions that require prior Space-mark selection.
	// The dispatcher surfaces a flash hint when a Bulk action
	// fires with no rows marked rather than silently no-oping.
	Bulk bool
}

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

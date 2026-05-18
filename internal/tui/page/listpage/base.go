// SPDX-License-Identifier: Apache-2.0

// Package listpage holds the shared base for the list-style pages
// (alerts, silences, groups, receivers). Base carries the nine
// type-independent fields every list page needs and (in later
// commits) exposes the helpers shared at 3+ callers. Pages embed
// Base directly; Base does NOT implement tea.Model — each page
// keeps its own Update/View/Init and calls into Base explicitly.
// See ADR 0013.
//
// Fields are exported so embedders in sibling packages can access
// them via promotion. The fields are simple value state with no
// invariants — getters/setters would be boilerplate.
package listpage

// Base is the embedded substruct that holds the nine universal
// list-page fields. Recompute is the per-page recompute callback
// wired by each page's constructor; Base methods that mutate state
// requiring a view rebuild call it instead of touching the page
// directly. Nil-safe in this commit (no Base method yet calls it);
// later commits that introduce such methods must validate non-nil
// at the page constructor.
type Base struct {
	Cursor int
	// TopRow tracks the first visible row index. The renderer
	// reconciles it with Cursor every frame so the cursor stays
	// inside the visible window.
	TopRow int
	// BodyHeight is the table-row capacity snapshotted on the most
	// recent View. Zero before the first WindowSizeMsg; handlers
	// fall back to 10/20 so a keystroke that beats the initial
	// WindowSizeMsg still moves a sane distance.
	BodyHeight int
	Filter     string
	// PreFilter is the pre-prompt snapshot the page restores on
	// PromptCancelledMsg{Mode: PromptFilter}. Nil iff no filter
	// prompt is open — invariant relies on the App auto-forwarding
	// PromptOpenedMsg to the top page.
	PreFilter *string
	Scope     string
	// Paused, when true, suppresses the byTenant/recompute branch
	// on incoming poll.DataMsg so the table stops updating under
	// the cursor mid-read. Toggled by `w` (watch mode).
	Paused bool
	// BackendHealth holds the per-tenant transport state for the
	// error band. An entry exists only while the tenant is not
	// connected — HandleBackendStatusMsg clears the row on
	// recovery. The renderer collapses the in-scope subset into a
	// one-line error band above the table. See ADR-0014.
	BackendHealth map[string]BackendHealth
	// Tenants is the canonical configured-backend list. Drives the
	// TENANT-column visibility so a tenant that never replies still
	// counts toward "is this a multi-tenant fleet?".
	Tenants   []string
	Recompute func()
}

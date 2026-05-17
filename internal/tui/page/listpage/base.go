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
	Cursor, TopRow, BodyHeight int
	Filter                     string
	PreFilter                  *string
	Scope                      string
	Paused                     bool
	LastErrors                 map[string]string
	Tenants                    []string
	Recompute                  func()
}

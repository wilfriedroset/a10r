// SPDX-License-Identifier: Apache-2.0

// Package listpage holds the shared base for the list-style pages
// (alerts, silences, receivers). Helpers earn their place at
// 3+ callers. Base does NOT implement tea.Model — pages embed it and
// call in explicitly. See ADR 0013. Cursor state lives in the
// embedded cursor.Window so the reconcile-on-change invariant is a
// type property, not a convention — see ADR 0016.
package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"

	tea "charm.land/bubbletea/v2"
)

// Base holds the type-independent fields every list page needs.
// Recompute is the per-page rebuild callback wired by each
// constructor.
type Base struct {
	cursor.Window
	Filter string
	// PreFilter is the pre-prompt snapshot restored on filter
	// cancel. Nil iff no filter prompt is open — relies on the App
	// auto-forwarding PromptOpenedMsg to the top page.
	PreFilter *string
	Scope     string
	// Paused suppresses the recompute branch on poll.DataMsg so the
	// table stops updating under the cursor mid-read. Toggled by `w`.
	Paused bool
	// BackendHealth holds per-tenant transport state for the error
	// band; an entry exists only while the tenant is not connected.
	// See ADR-0014.
	BackendHealth map[string]BackendHealth
	// Tenants is the canonical configured-backend list. Drives
	// TENANT-column visibility so a tenant that never replies still
	// counts toward "is this a multi-tenant fleet?".
	Tenants   []string
	Recompute func()
	// RowCount returns the rows the page currently presents. Panics
	// on nil when GoToFirstRowMsg arrives — see ADR-0018.
	RowCount func() int
	// SnapshotFocus captures the current row's identity so the next
	// recompute re-resolves it. Panics on nil when GoToFirstRowMsg
	// arrives — see ADR-0018.
	SnapshotFocus func()
	// SetTimeFormat applies a TimeFormatChangedMsg. Nil on pages
	// that render no time (receivers); nil is treated as a
	// fall-through by HandleSidebandMsg — see ADR-0018.
	SetTimeFormat func(timerender.Format)
	// SetStateFormat applies a StateFormatChangedMsg. Nil except on
	// the alerts list and group detail; nil falls through — see
	// ADR-0018.
	SetStateFormat func(stateformat.Format)
	// ClearMarks runs the page's mark-clearing routine and returns
	// any follow-up flash command. Nil on pages without marks; nil
	// falls through — see ADR-0018.
	ClearMarks func() tea.Cmd
}

// SPDX-License-Identifier: Apache-2.0

// Package listpage holds the shared base for the list-style pages
// (alerts, silences, groups, receivers). Base carries the
// type-independent fields every list page needs and exposes the
// helpers shared at 3+ callers. Pages embed Base directly; Base
// does NOT implement tea.Model — each page keeps its own
// Update/View/Init and calls into Base explicitly. See ADR 0013.
//
// Cursor / topRow / bodyHeight are bundled into the embedded
// cursor.Window so pages access them through promoted methods
// (Index, TopRow, MoveCursor, SetIndex, SetViewport, Clamp) and
// the reconcile-on-change invariant is enforced by the type rather
// than by convention. See ADR 0016.
package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/page/cursor"
	"github.com/wilfriedroset/a10r/internal/tui/stateformat"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"

	tea "charm.land/bubbletea/v2"
)

// Base is the embedded substruct that holds the universal list-page
// fields. Recompute is the per-page recompute callback wired by
// each page's constructor.
type Base struct {
	cursor.Window
	Filter string
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
	// RowCount returns the number of rows the page currently
	// presents. Wired by each page constructor; used by
	// HandleSidebandMsg to clamp the cursor on GoToFirstRowMsg.
	// Panics on nil when GoToFirstRowMsg arrives — see ADR-0018.
	RowCount func() int
	// SnapshotFocus captures the focus identity of the current
	// row so the next recompute can re-resolve it. Wired by each
	// page constructor; panics on nil when GoToFirstRowMsg
	// arrives — see ADR-0018.
	SnapshotFocus func()
	// SetTimeFormat applies the new format on a TimeFormatChangedMsg.
	// Nil on pages that do not render time (groups, receivers);
	// HandleSidebandMsg returns handled=false in that case so the
	// page's main switch sees the message unchanged — see ADR-0018.
	SetTimeFormat func(timerender.Format)
	// SetStateFormat applies the new density on a
	// StateFormatChangedMsg. Nil on pages that do not render the
	// state breakdown (only the alerts list and group detail do);
	// HandleSidebandMsg returns handled=false in that case so the
	// message passes through unchanged — same nil-as-fall-through
	// contract as SetTimeFormat (ADR-0018).
	SetStateFormat func(stateformat.Format)
	// ClearMarks runs the page's mark-clearing routine on a
	// ClearMarksMsg and returns any follow-up flash command. Nil
	// on pages without marks (groups, receivers); HandleSidebandMsg
	// returns handled=false in that case — see ADR-0018.
	ClearMarks func() tea.Cmd
}

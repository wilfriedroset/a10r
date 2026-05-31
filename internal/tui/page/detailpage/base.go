// SPDX-License-Identifier: Apache-2.0

// Package detailpage holds the shared base for the read-only detail
// pages (alert, silence, tenantconfig). Base carries the universal
// 1D-scroll state every detail page needs and exposes the helpers
// shared at 3+ callers. Pages embed *Base directly; Base does NOT
// implement tea.Model — each page keeps its own Update/View/Init
// and calls into Base explicitly. See ADR 0022.
//
// Nil discipline mirrors ADR-0018: universal callbacks (none today;
// all detail-page universals are message-only) panic on nil at the
// moment of need; optional callbacks (SetTimeFormat, InitCmd) treat a
// nil hook as a fall-through (handled=false) so the page's main switch
// sees the message unchanged.
package detailpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Base is the embedded substruct that holds the universal detail-
// page fields and the optional callbacks each page wires at
// construction.
//
// Scroll / BodyHeight are exposed as exported fields rather than
// hidden behind a Window-style type because detail-page scroll is
// 1D (a single index of the first visible line) and the dispatch
// surface is tiny — j/k/G/Ctrl+D/U/F/B and an `app.GoToFirstRowMsg`
// reset. The two list-page concerns Window absorbs (cursor row
// position and topRow reconciliation) don't apply here, so a
// dedicated value type would be a half-abstraction.
type Base struct {
	// Scroll is the index of the first visible body line. j/k/G/Ctrl+D/U/F/B
	// walk it; the renderer reconciles against the body height every
	// frame so the user can never scroll past the bottom.
	Scroll int

	// BodyHeight is the viewport size snapshotted on the most recent
	// View call. Ctrl+D/U step half it; Ctrl+F/B step body-2. Zero
	// before the first render — handlers fall back to 10 / 20 via
	// cursor.HalfPageStep / cursor.FullPageStep.
	BodyHeight int

	// SetTimeFormat applies the new format on an
	// app.TimeFormatChangedMsg. Nil on detail pages that do not
	// render relative times (silence, tenantconfig);
	// HandleSidebandMsg returns handled=false in that case so the
	// page's main switch sees the message unchanged. See ADR-0022.
	SetTimeFormat func(timerender.Format)

	// InitCmd is the optional periodic-refresh / lazy-fetch Cmd a
	// detail page returns from Init. Nil on pages that have no
	// startup-side effect (alert, silence). The tenantconfig page
	// wires its /api/v2/status fetch through this hook. See ADR-0022.
	InitCmd func() tea.Cmd
}

// Init implements the no-op Bubble Tea Init form shared by detail
// pages, delegating to the optional InitCmd hook when wired. Pages
// override this directly if they need a richer Init than a single
// Cmd.
func (b *Base) Init() tea.Cmd {
	if b.InitCmd == nil {
		return nil
	}
	return b.InitCmd()
}

// Close implements the no-op Close form shared by detail pages.
// Pages that own background workers (tenantconfig's /status fetch)
// override this directly.
func (*Base) Close() tea.Cmd { return nil }

// HeaderContent implements the empty-by-default HeaderContent form.
// Detail-page titles already surface the scope/identifier; an extra
// subtitle would duplicate what's a glance away. Pages override
// this when they have transient header copy (tenantconfig's
// "fetching…" line).
func (*Base) HeaderContent() string { return "" }

// Footer implements the empty-by-default Footer form. Detail pages
// don't surface ambient state in the bottom border today.
func (*Base) Footer() string { return "" }

// SPDX-License-Identifier: Apache-2.0

// Package detailpage holds the shared 1D-scroll base for the read-only
// detail pages (alert, silence, tenantconfig). Pages embed *Base and
// call into it explicitly; Base is not a tea.Model. See ADR 0022.
//
// Nil discipline mirrors ADR-0018: optional callbacks (SetTimeFormat,
// InitCmd) treat a nil hook as a fall-through so the page's main switch
// sees the message unchanged.
package detailpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

// Base holds the universal detail-page fields and the optional
// callbacks each page wires at construction. Scroll state is exposed
// as plain exported fields rather than behind a Window type because
// detail-page scroll is 1D and Window's cursor/topRow concerns don't
// apply.
type Base struct {
	// Scroll is the index of the first visible body line; the renderer
	// reconciles it against body height each frame so the user can never
	// scroll past the bottom.
	Scroll int

	// BodyHeight is the viewport size from the most recent View. Zero
	// before the first render — handlers fall back via
	// cursor.HalfPageStep / cursor.FullPageStep.
	BodyHeight int

	// SetTimeFormat applies a new format on app.TimeFormatChangedMsg.
	// Nil on pages that render no relative times. See ADR-0022.
	SetTimeFormat func(timerender.Format)

	// InitCmd is the optional periodic-refresh / lazy-fetch Cmd a detail
	// page returns from Init (tenantconfig's /api/v2/status fetch).
	// See ADR-0022.
	InitCmd func() tea.Cmd
}

// Init delegates to the optional InitCmd hook when wired.
func (b *Base) Init() tea.Cmd {
	if b.InitCmd == nil {
		return nil
	}
	return b.InitCmd()
}

// Close is the no-op default; pages with background workers override it.
func (*Base) Close() tea.Cmd { return nil }

// HeaderContent is empty by default; the title already carries the
// scope/identifier. Pages with transient header copy override it.
func (*Base) HeaderContent() string { return "" }

// Footer is empty by default; detail pages surface no ambient state.
func (*Base) Footer() string { return "" }

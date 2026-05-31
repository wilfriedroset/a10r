// SPDX-License-Identifier: Apache-2.0

package detailpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// HandleSidebandMsg consumes the cross-cutting messages every
// detail page sees: the universal `app.GoToFirstRowMsg` scroll-
// home reset and the optional `app.TimeFormatChangedMsg` (consumed
// by pages that render relative times). Each detail page's Update
// calls this first and short-circuits when handled=true so the main
// switch stays focused on page-specific routing.
//
// Universal cases panic on a nil dependency the same way list
// pages do — a silently-skipped scroll-home would lose user
// intent without any observable failure. The optional
// TimeFormatChangedMsg treats a nil callback as a fall-through
// (handled=false) so pages without the feature pass through without
// per-page switch scaffolding. See ADR 0022.
func (b *Base) HandleSidebandMsg(msg tea.Msg) (handled bool, cmd tea.Cmd) {
	switch m := msg.(type) {
	case app.GoToFirstRowMsg:
		b.Scroll = 0
		return true, nil
	case app.TimeFormatChangedMsg:
		if b.SetTimeFormat == nil {
			return false, nil
		}
		b.SetTimeFormat(m.Format)
		return true, nil
	}
	return false, nil
}

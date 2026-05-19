// SPDX-License-Identifier: Apache-2.0

package detailpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// HandleSidebandMsg consumes the cross-cutting messages every detail
// page sees today: the universal `app.GoToFirstRowMsg` scroll-home
// reset, the optional `app.TimeFormatChangedMsg` (only the alert
// page renders relative times today), and the optional
// `modal.ResultMsg` (only the alert page opens modals today). Each
// detail page's Update calls this first and short-circuits when
// handled=true so the main switch stays focused on page-specific
// routing.
//
// Universal cases panic on a nil dependency the same way list
// pages do — a silently-skipped scroll-home would lose user intent
// without any observable failure. Optional cases
// (TimeFormatChangedMsg, ModalResultMsg) treat a nil callback as a
// fall-through (handled=false) so pages without the corresponding
// feature preserve their today behaviour without per-page switch
// scaffolding. See ADR 0022.
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
	if r, ok := msg.(modal.ResultMsg); ok {
		if b.OnModalResult == nil {
			return false, nil
		}
		return b.OnModalResult(r)
	}
	return false, nil
}

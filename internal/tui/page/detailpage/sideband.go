// SPDX-License-Identifier: Apache-2.0

package detailpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// HandleSidebandMsg consumes the cross-cutting messages every detail
// page sees: the universal app.GoToFirstRowMsg scroll-home reset and
// the optional app.TimeFormatChangedMsg. Pages call this first and
// short-circuit on handled=true.
//
// A nil SetTimeFormat is a fall-through (handled=false) so pages
// without the feature pass through without per-page scaffolding.
// See ADR 0022.
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

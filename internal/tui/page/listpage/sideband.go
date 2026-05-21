// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// HandleSidebandMsg consumes the four app-level cross-cutting
// messages every list page sees but routes uniformly: scope
// change, time-format toggle, "go to first row" chord completion,
// and global mark-clearing. Each list page's Update calls this
// first and short-circuits when handled=true so the main switch
// stays focused on page-specific routing.
//
// Optional cases (TimeFormatChangedMsg, ClearMarksMsg) treat a
// nil callback as a fall-through (handled=false) so pages without
// the corresponding feature — groups and receivers don't render
// times; groups and receivers don't track marks — pass through
// without per-page switch scaffolding. See ADR 0018.
func (b *Base) HandleSidebandMsg(msg tea.Msg) (handled bool, cmd tea.Cmd) {
	switch m := msg.(type) {
	case app.ScopeChangedMsg:
		b.HandleScopeChangedMsg(m)
		return true, nil
	case app.GoToFirstRowMsg:
		if b.RowCount == nil {
			panic("listpage.Base.HandleSidebandMsg: RowCount callback not wired by page constructor")
		}
		if b.SnapshotFocus == nil {
			panic("listpage.Base.HandleSidebandMsg: SnapshotFocus callback not wired by page constructor")
		}
		b.SetIndex(0, b.RowCount())
		b.SnapshotFocus()
		return true, nil
	case app.TimeFormatChangedMsg:
		if b.SetTimeFormat == nil {
			return false, nil
		}
		b.SetTimeFormat(m.Format)
		return true, nil
	case app.ClearMarksMsg:
		if b.ClearMarks == nil {
			return false, nil
		}
		return true, b.ClearMarks()
	}
	return false, nil
}

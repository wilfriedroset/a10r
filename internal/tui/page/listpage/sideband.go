// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// HandleSidebandMsg routes the five cross-cutting messages every list
// page handles uniformly (scope, time/state format, go-to-first-row,
// mark-clear). Pages call it first and short-circuit on handled=true.
// For the optional cases a nil callback is a fall-through
// (handled=false), so pages lacking that feature pass through without
// per-page switch scaffolding. See ADR 0018.
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
	case app.StateFormatChangedMsg:
		if b.SetStateFormat == nil {
			return false, nil
		}
		b.SetStateFormat(m.Format)
		return true, nil
	case app.ClearMarksMsg:
		if b.ClearMarks == nil {
			return false, nil
		}
		return true, b.ClearMarks()
	}
	return false, nil
}

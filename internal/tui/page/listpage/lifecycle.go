// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// ToggleWatch flips Base.Paused. Resuming clears the PausedRefresh
// one-shot — paused→running is a clean state, not a pending refresh.
func ToggleWatch(b *Base, ui *PollingUI) {
	b.Paused = !b.Paused
	if !b.Paused {
		ui.PausedRefresh = false
	}
}

// RequestRefresh emits a RefreshRequestedMsg and re-kicks the spinner
// Tick chain. When paused, sets PausedRefresh so the next DataMsg is
// honoured exactly once — a deliberate pull should show fresh data
// even with watch off. Empty Scope normalises to ScopeAll so the
// wiring layer sees the value the renderer uses.
func RequestRefresh(b *Base, ui *PollingUI, resource string) tea.Cmd {
	ui.Refreshing = true
	if b.Paused {
		ui.PausedRefresh = true
	}
	scope := b.Scope
	if scope == "" {
		scope = ScopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: resource, Scope: scope}
	}
	return tea.Batch(emit, ui.Spinner.Tick)
}

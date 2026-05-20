// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// ToggleWatch flips Base.Paused for a list page. Coming out of
// paused clears the PausedRefresh one-shot — paused→running is a
// clean state, not a pending refresh.
func ToggleWatch(b *Base, ui *PollingUI) {
	b.Paused = !b.Paused
	if !b.Paused {
		ui.PausedRefresh = false
	}
}

// RequestRefresh emits a RefreshRequestedMsg for the named resource
// and re-kicks the spinner Tick chain. When the page is paused, sets
// PausedRefresh so the next incoming DataMsg is honoured exactly
// once — the operator pulled it deliberately and expects to see
// fresh data even though watch mode is off. Empty Scope normalises
// to scopeAll so the wiring layer sees the same value the renderer
// uses.
func RequestRefresh(b *Base, ui *PollingUI, resource string) tea.Cmd {
	ui.Refreshing = true
	if b.Paused {
		ui.PausedRefresh = true
	}
	scope := b.Scope
	if scope == "" {
		scope = scopeAll
	}
	emit := func() tea.Msg {
		return app.RefreshRequestedMsg{Resource: resource, Scope: scope}
	}
	return tea.Batch(emit, ui.Spinner.Tick)
}

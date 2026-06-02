// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// HandleFilterPrompt centralises the filter-prompt lifecycle so each
// page's Update stays focused on its typed data. On open it snapshots
// then clears the filter so the user types against the unfiltered
// list; cancel restores that snapshot. Command-mode prompts pass
// through unchanged — pages only own filter mode here. Panics on nil
// Recompute, which a constructor must wire before any prompt arrives.
func (b *Base) HandleFilterPrompt(msg tea.Msg) {
	if b.Recompute == nil {
		panic("listpage.Base.HandleFilterPrompt: Recompute callback not wired by page constructor")
	}
	switch m := msg.(type) {
	case footer.PromptOpenedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		snap := b.Filter
		b.PreFilter = &snap
		if b.Filter != "" {
			b.Filter = ""
			b.Recompute()
		}
	case footer.PromptChangedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		b.Filter = m.Value
		b.Recompute()
	case footer.PromptSubmittedMsg:
		if m.Mode != footer.PromptFilter {
			return
		}
		b.Filter = m.Value
		b.PreFilter = nil
		b.Recompute()
	case footer.PromptCancelledMsg:
		if m.Mode != footer.PromptFilter || b.PreFilter == nil {
			return
		}
		b.Filter = *b.PreFilter
		b.PreFilter = nil
		b.Recompute()
	}
}

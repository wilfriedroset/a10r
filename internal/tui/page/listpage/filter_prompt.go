// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// HandleFilterPrompt centralises the four filter-prompt lifecycle
// messages so each page's Update stays focused on its own typed
// data. Branches:
//
//   - Opened: snapshot the active filter and clear it so the user
//     types against the unfiltered list (live filter rebuilds it
//     keystroke-by-keystroke).
//   - Changed: apply the in-flight value live.
//   - Submitted: commit the typed value (possibly empty, meaning
//     "clear the filter"); drop the pre-prompt snapshot.
//   - Cancelled: restore the snapshot.
//
// Command-mode prompt messages slip through unchanged — pages
// only own filter mode at this layer.
//
// Panics with a clear message when called before the page wires
// Recompute. Each page constructor must set b.Recompute = p.recompute
// (or the equivalent rebuild) before any filter prompt can arrive.
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

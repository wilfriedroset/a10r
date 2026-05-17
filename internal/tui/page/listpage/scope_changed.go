// SPDX-License-Identifier: Apache-2.0

package listpage

import "github.com/wilfriedroset/a10r/internal/tui/app"

// HandleScopeChangedMsg consumes the app-level scope-change
// sideband message. Updates b.Scope and invokes the page's
// Recompute callback so the view rebuilds against the new
// tenant selection.
//
// Panics with a clear message when called before the page wires
// Recompute. Each page constructor must set b.Recompute before
// any ScopeChangedMsg can arrive.
func (b *Base) HandleScopeChangedMsg(msg app.ScopeChangedMsg) {
	if b.Recompute == nil {
		panic("listpage.Base.HandleScopeChangedMsg: Recompute callback not wired by page constructor")
	}
	b.Scope = msg.Scope
	b.Recompute()
}

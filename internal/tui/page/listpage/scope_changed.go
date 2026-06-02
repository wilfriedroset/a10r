// SPDX-License-Identifier: Apache-2.0

package listpage

import "github.com/wilfriedroset/a10r/internal/tui/app"

// HandleScopeChangedMsg updates b.Scope and rebuilds via Recompute.
// Panics on nil Recompute, which a constructor must wire before any
// ScopeChangedMsg arrives.
func (b *Base) HandleScopeChangedMsg(msg app.ScopeChangedMsg) {
	if b.Recompute == nil {
		panic("listpage.Base.HandleScopeChangedMsg: Recompute callback not wired by page constructor")
	}
	b.Scope = msg.Scope
	b.Recompute()
}

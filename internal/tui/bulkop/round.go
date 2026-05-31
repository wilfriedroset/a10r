// SPDX-License-Identifier: Apache-2.0

package bulkop

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// BeginRound cancels the previous in-flight round (if any) and derives
// a fresh cancellable context from parent for a new one. parent==nil
// falls back to context.Background(). The returned cancel must be both
// stored on the page (so Close can cancel an in-flight round) and
// handed to RunRound (so a completed round releases its ctx subtree
// even after a newer round has overwritten the stored cancel).
func BeginRound(parent context.Context, prev context.CancelFunc) (context.Context, context.CancelFunc) {
	if prev != nil {
		prev()
	}
	if parent == nil {
		parent = context.Background()
	}
	return context.WithCancel(parent)
}

// RunRound wraps a dispatch Cmd so cancel fires the moment dispatch
// returns, releasing the round's ctx subtree regardless of whether the
// page's stored cancel has since been overwritten by a newer round.
func RunRound(cancel context.CancelFunc, dispatch tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return dispatch()
	}
}

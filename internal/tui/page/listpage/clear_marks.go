// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// ClearMarks drops every mark and any open visual range, and returns
// the flash that says so. The range goes first and unconditionally,
// for Esc parity: an anchor must not outlive the key that cancels it,
// or it re-previews rows as marked that nothing marked. Nothing
// marked flashes nothing: a message for a key press that changed
// nothing trains the operator to ignore the flash line.
func ClearMarks(b *Base, marks map[string]struct{}) tea.Cmd {
	b.Visual.Cancel()
	if len(marks) == 0 {
		return nil
	}
	clear(marks)
	return footer.ShowFlash(footer.FlashInfo, "marks cleared")
}

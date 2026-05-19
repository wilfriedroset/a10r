// SPDX-License-Identifier: Apache-2.0

package boot

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
)

// QuitFilter returns the bubbletea WithFilter callback that
// translates raw tea.QuitMsg / tea.InterruptMsg pushed by the
// runtime's signal handler (SIGTERM / SIGINT — kubernetes, systemd,
// supervisor shutdown) into app.QuitRequestedMsg so the page-stack
// Close cascade runs before bubbletea tears the program down.
//
// bubbletea's eventLoop short-circuits on QuitMsg / InterruptMsg
// BEFORE dispatching to Update (vendor: tea.go eventLoop), so
// without this hook the cascade — every page's Close(), every
// cancel func for in-flight bulk fanouts / silence-form writes /
// editor updates — never runs and the workers leak for the full
// HTTP timeout window.
//
// The filter consults a.Quitting() so the App's own authorised
// quit (QuitRequestedMsg → quitWithCleanup → tea.Quit cmd) still
// reaches bubbletea unchanged: once the cascade has run, the next
// QuitMsg must be allowed through so the program actually exits.
// Without that gate, the filter would loop QuitMsg back into
// QuitRequestedMsg forever.
//
// Non-quit messages pass through untouched — the filter is a narrow
// translation, not a general message rewriter.
func QuitFilter(a *app.App) func(tea.Model, tea.Msg) tea.Msg {
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		switch msg.(type) {
		case tea.QuitMsg, tea.InterruptMsg:
			if a.Quitting() {
				return msg
			}
			return app.QuitRequestedMsg{}
		}
		return msg
	}
}

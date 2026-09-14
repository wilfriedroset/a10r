// SPDX-License-Identifier: Apache-2.0

// Package clipboard holds the copy-to-clipboard seam the detail pages
// share, plus the field picker they open on `Y`. It lives outside the
// pages because alert-detail already imports silence-detail to push it,
// so hosting the seam on the alert page would close an import cycle.
package clipboard

import (
	tea "charm.land/bubbletea/v2"
)

// Clipboard is the copy-to-clipboard seam. The Cmd runs in the
// bubbletea loop because OSC52 must go through the renderer, not a
// raw stdout write; it is fire-and-forget, so no failure to report.
type Clipboard interface {
	Copy(s string) tea.Cmd
}

// OSC52 is the default Clipboard, using the terminal's OSC52
// sequence so it works over SSH and without an X/Wayland display.
type OSC52 struct{}

// Copy implements Clipboard.
func (OSC52) Copy(s string) tea.Cmd { return tea.SetClipboard(s) }

// Resolve substitutes the OSC52 default for a nil injection, so every
// page's nil-Clipboard option resolves the same way.
func Resolve(c Clipboard) Clipboard {
	if c == nil {
		return OSC52{}
	}
	return c
}

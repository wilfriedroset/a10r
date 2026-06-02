// SPDX-License-Identifier: Apache-2.0

package listpage

import "charm.land/lipgloss/v2"

// Pane pads body to exactly width×height with no background so the
// terminal default shows through (fg-only chrome, see
// feedback_no_palette_bg.md). Used for the empty View branch so the
// empty-state message fills the pane instead of one line.
func Pane(width, height int, body string) string {
	return lipgloss.NewStyle().Width(width).Height(height).Render(body)
}

// Wrap width-pads body with no background and no height constraint
// (rendered rows set the line count). Used for the populated View
// branch.
func Wrap(width int, body string) string {
	return lipgloss.NewStyle().Width(width).Render(body)
}

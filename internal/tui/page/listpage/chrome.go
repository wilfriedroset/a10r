// SPDX-License-Identifier: Apache-2.0

package listpage

import "charm.land/lipgloss/v2"

// Pane pads body to exactly width×height with no background styling,
// so the terminal default bg shows through. Used for the empty
// branch of each list page's View — the empty-state message fills
// the full pane instead of shrinking to one line. See
// feedback_no_palette_bg.md for the fg-only chrome rationale.
func Pane(width, height int, body string) string {
	return lipgloss.NewStyle().Width(width).Height(height).Render(body)
}

// Wrap width-pads body without applying a background style or a
// height constraint — the rendered rows determine the natural line
// count. Used for the populated branch of each list page's View.
func Wrap(width int, body string) string {
	return lipgloss.NewStyle().Width(width).Render(body)
}

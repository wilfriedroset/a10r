// SPDX-License-Identifier: Apache-2.0

package detailpage

// Visible records the latest viewport height onto Base, clamps Scroll
// against the new line count, and returns the visible window of lines
// the renderer should draw. Detail-page View() bodies share this
// pipeline byte-for-byte — see ADR 0022's inclusion rule.
func (b *Base) Visible(lines []string, height int) []string {
	b.BodyHeight = height
	b.ReconcileScroll(len(lines), height)
	end := min(b.Scroll+height, len(lines))
	return lines[b.Scroll:end]
}

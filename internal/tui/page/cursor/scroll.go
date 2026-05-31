// SPDX-License-Identifier: Apache-2.0

package cursor

// ReconcileScroll computes the topRow that keeps the cursor visible in
// a maxRows-tall window over a totalRows-long list, clamped so the
// window never scrolls past the last position. Returns the new topRow.
// Pure, easy to fuzz.
func ReconcileScroll(cursor, topRow, maxRows, totalRows int) int {
	if cursor < topRow {
		topRow = cursor
	}
	if cursor >= topRow+maxRows {
		topRow = cursor - maxRows + 1
	}
	maxTop := max(totalRows-maxRows, 0)
	if topRow > maxTop {
		topRow = maxTop
	}
	if topRow < 0 {
		topRow = 0
	}
	return topRow
}

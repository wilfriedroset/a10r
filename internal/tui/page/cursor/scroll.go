// SPDX-License-Identifier: Apache-2.0

package cursor

// ReconcileScroll computes the topRow that keeps the cursor visible
// in a maxRows-tall window over a totalRows-long row list. Returns
// the new topRow; pages assign it back to their own field.
//
// Three rules apply in order:
//
//   - if cursor is above the window, topRow snaps to cursor;
//   - if cursor is below the window, topRow advances to keep cursor
//     on the last visible row;
//   - finally topRow is clamped into [0, max(totalRows-maxRows, 0)]
//     so the window never scrolls past the last possible position.
//
// Pure function — no side effects, easy to fuzz.
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

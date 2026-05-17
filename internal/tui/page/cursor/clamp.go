// SPDX-License-Identifier: Apache-2.0

package cursor

// Clamp returns the cursor index bounded to [0, itemCount-1]. When the
// list is empty (itemCount <= 0) the result is 0, giving callers a
// safe default they can hand to a renderer without an extra guard.
//
// Three rules apply in order:
//
//   - empty list (itemCount <= 0) collapses to 0;
//   - cursor below 0 clamps up to 0;
//   - cursor at or past itemCount clamps down to itemCount-1.
//
// Pure function — no side effects, easy to fuzz.
func Clamp(cursor, itemCount int) int {
	if itemCount <= 0 {
		return 0
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= itemCount {
		return itemCount - 1
	}
	return cursor
}

// SPDX-License-Identifier: Apache-2.0

package cursor

// Clamp bounds the cursor to [0, itemCount-1]. An empty list
// (itemCount <= 0) returns 0, a safe default callers can render
// without an extra guard. Pure, easy to fuzz.
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

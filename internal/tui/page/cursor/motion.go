// SPDX-License-Identifier: Apache-2.0

// Package cursor holds the vim-motion / page-step helpers shared
// across every page that renders a row table or a scrolling viewer.
// Pages keep ownership of the cursor / scroll field; this package is
// pure functions over (key, cursor, bounds) -> (newCursor, handled).
package cursor

// HandleMotion processes the vim-style table motion bindings
// (j/down and k/up walking, G to the last row, Ctrl+D/U half-page,
// Ctrl+F/B full-page) and returns the new cursor index. handled
// reports whether the key was consumed; pages should fall through
// to their page-specific bindings on a false return.
//
// rowCount is the number of selectable rows (0 is fine — every
// motion clamps to a no-op); halfStep / fullStep are the half- and
// full-page distances pages compute via HalfPageStep / FullPageStep.
//
// Pages that snapshot a focus row on cursor moves should do so on a
// true return; this helper is intentionally focus-agnostic so the
// tenant page (no focus tracking) can use it unchanged.
func HandleMotion(key string, cursor, rowCount, halfStep, fullStep int) (newCursor int, handled bool) {
	last := max(rowCount-1, 0)
	switch key {
	case "j", "down":
		if cursor < last {
			return cursor + 1, true
		}
		return cursor, true
	case "k", "up":
		if cursor > 0 {
			return cursor - 1, true
		}
		return cursor, true
	case "G":
		return last, true
	case "ctrl+d":
		return min(cursor+halfStep, last), true
	case "ctrl+u":
		return max(cursor-halfStep, 0), true
	case "ctrl+f":
		return min(cursor+fullStep, last), true
	case "ctrl+b":
		return max(cursor-fullStep, 0), true
	}
	return cursor, false
}

// HalfPageStep returns the Ctrl+D / Ctrl+U distance: half the body
// height, with a sane floor when the page hasn't been sized yet.
// Same shape every page uses, lifted here so the seven page files
// don't each carry their own copy.
func HalfPageStep(bodyHeight int) int {
	if bodyHeight < 2 {
		return 10
	}
	return max(bodyHeight/2, 1)
}

// FullPageStep returns the Ctrl+F / Ctrl+B distance: a full body
// height minus two for header / footer overlap, with the same kind
// of floor as HalfPageStep.
func FullPageStep(bodyHeight int) int {
	if bodyHeight < 4 {
		return 20
	}
	return max(bodyHeight-2, 1)
}

// SPDX-License-Identifier: Apache-2.0

package cursor

// Window bundles a list page's cursor, top-of-visible-window, and
// body-height state under one type, with every state-changing
// method reconciling topRow against (cursor, items, bodyHeight)
// internally. The zero value is usable: cursor=0, topRow=0,
// bodyHeight=0 with the fallback applied inside MoveCursor for
// the pre-first-WindowSizeMsg case.
//
// See ADR-0016 for the rationale and the rejected alternatives.
type Window struct {
	cursor     int
	topRow     int
	bodyHeight int
}

// NewWindow constructs a Window with the given seed state. Tests
// use this to set up scenarios; production code embeds a zero-value
// Window and lets SetViewport / MoveCursor populate it.
func NewWindow(cursor, topRow, bodyHeight int) Window {
	return Window{cursor: cursor, topRow: topRow, bodyHeight: bodyHeight}
}

// Index returns the current cursor row.
func (w *Window) Index() int { return w.cursor }

// TopRow returns the first visible row index.
func (w *Window) TopRow() int { return w.topRow }

// MoveCursor processes the vim-style table motion bindings
// (j/down, k/up, G, Ctrl+D/U, Ctrl+F/B) and reconciles topRow.
//
// handled reports whether the key was consumed (keymap-walk gate);
// changed reports whether the cursor index actually moved
// (focus-snapshot gate). They diverge on j-at-last-row and
// k-at-row-0: handled=true, changed=false.
func (w *Window) MoveCursor(key string, items int) (changed, handled bool) {
	last := max(items-1, 0)
	prev := w.cursor
	switch key {
	case "j", "down":
		if w.cursor < last {
			w.cursor++
		}
		handled = true
	case "k", "up":
		if w.cursor > 0 {
			w.cursor--
		}
		handled = true
	case "G":
		w.cursor = last
		handled = true
	case "ctrl+d":
		w.cursor = min(w.cursor+w.halfStep(), last)
		handled = true
	case "ctrl+u":
		w.cursor = max(w.cursor-w.halfStep(), 0)
		handled = true
	case "ctrl+f":
		w.cursor = min(w.cursor+w.fullStep(), last)
		handled = true
	case "ctrl+b":
		w.cursor = max(w.cursor-w.fullStep(), 0)
		handled = true
	default:
		return false, false
	}
	if items <= 0 {
		w.cursor = 0
	}
	changed = w.cursor != prev
	w.reconcile(items)
	return changed, handled
}

// SetIndex moves the cursor to i (clamped into [0, items-1]) and
// reconciles topRow. Used by silences/alerts to land the cursor on
// the row matching the focus snapshot post-recompute.
func (w *Window) SetIndex(i, items int) {
	w.cursor = Clamp(i, items)
	w.reconcile(items)
}

// SetViewport records the latest body height and reconciles topRow
// so the cursor stays visible across terminal resizes.
func (w *Window) SetViewport(height, items int) {
	w.bodyHeight = height
	w.cursor = Clamp(w.cursor, items)
	w.reconcile(items)
}

// Clamp bounds the cursor into [0, items-1] and reconciles topRow.
// Pages call this after a recompute that may have shrunk the item
// list under the cursor.
func (w *Window) Clamp(items int) {
	w.cursor = Clamp(w.cursor, items)
	w.reconcile(items)
}

func (w *Window) reconcile(items int) {
	if w.bodyHeight <= 0 {
		return
	}
	w.topRow = ReconcileScroll(w.cursor, w.topRow, w.bodyHeight, items)
}

func (w *Window) halfStep() int {
	if w.bodyHeight < 2 {
		return 10
	}
	return max(w.bodyHeight/2, 1)
}

func (w *Window) fullStep() int {
	if w.bodyHeight < 4 {
		return 20
	}
	return max(w.bodyHeight-2, 1)
}

// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// HintVisualCancelled is the flash a page emits when a filter change
// or a poll refresh drops the anchor row. Without it the preview
// would vanish with no stated reason.
const HintVisualCancelled = "visual cancelled: anchor row left the view"

// ChipVisual is the header chip that advertises an open visual-mode
// range. Pages append it after their own chips.
const ChipVisual = "visual"

// Visual is the range-mark anchor: `V` drops it on the cursor row and
// a second `V` (or `Space`) marks every row between it and the
// cursor. The anchor is a row key, not an index, so a re-sort or a
// re-filter carries the preview with the row instead of sliding it
// onto unrelated rows — the way marks and cursor focus already
// follow. The zero value is "not in visual mode".
type Visual struct {
	anchor string
	on     bool
}

// On reports whether visual mode is active.
func (v *Visual) On() bool { return v.on }

// Start anchors visual mode on key. An empty key is a no-op, because
// an anchor that matches no row could never be resolved again.
func (v *Visual) Start(key string) {
	if key == "" {
		return
	}
	v.anchor, v.on = key, true
}

// Cancel leaves visual mode without touching marks.
func (v *Visual) Cancel() { v.anchor, v.on = "", false }

// VisualRange is the row span an open range previews, resolved
// against one frame's view. The zero value covers nothing, so a
// renderer can carry one without a second "is a range open" flag.
type VisualRange struct {
	lo, hi int
	open   bool
}

// Covers reports whether the row at view index i sits inside the
// range — the renderer's "draw this row as marked" test.
func (r VisualRange) Covers(i int) bool { return r.open && i >= r.lo && i <= r.hi }

// Open reports whether the range resolved to any row.
func (r VisualRange) Open() bool { return r.open }

// VisualPreview resolves the rows an open range covers: the inclusive
// span between the anchor row and the cursor. The range is closed
// when visual mode is off, when the cursor sits past the view, or
// when the anchor row left the view.
func VisualPreview[E any](b *Base, view []E, keyFn func(E) string) VisualRange {
	cursorIdx := b.Index()
	if !b.Visual.on || cursorIdx < 0 || cursorIdx >= len(view) {
		return VisualRange{}
	}
	anchorIdx := indexOfKey(view, b.Visual.anchor, keyFn)
	if anchorIdx < 0 {
		return VisualRange{}
	}
	return VisualRange{lo: min(anchorIdx, cursorIdx), hi: max(anchorIdx, cursorIdx), open: true}
}

// StartOrCommitVisual routes the `V` key: the first press anchors a
// range on the cursor row, the second marks every row the preview
// covers.
func StartOrCommitVisual[E any](b *Base, view []E, marks map[string]struct{}, keyFn func(E) string) {
	if CommitVisual(b, view, marks, keyFn) {
		return
	}
	if i := b.Index(); i >= 0 && i < len(view) {
		b.Visual.Start(keyFn(view[i]))
	}
}

// CommitVisual adds every key in the preview span to marks and leaves
// visual mode, then reports whether a range was open. It only ever
// adds: marks set outside the span survive, so a range composes with
// the rows the user picked one by one. The bulk verbs call it first so
// an open range reaches them as marks.
func CommitVisual[E any](b *Base, view []E, marks map[string]struct{}, keyFn func(E) string) bool {
	if !b.Visual.on {
		return false
	}
	r := VisualPreview(b, view, keyFn)
	b.Visual.Cancel()
	if !r.open {
		return true
	}
	for _, e := range view[r.lo : r.hi+1] {
		if k := keyFn(e); k != "" {
			marks[k] = struct{}{}
		}
	}
	return true
}

// MarkOrCommit routes the `Space` key: it commits an open range, or
// toggles the mark on the cursor row when there is none. An empty key
// is skipped, because a mark nothing can carry could never be undone.
func MarkOrCommit[E any](b *Base, view []E, marks map[string]struct{}, keyFn func(E) string) {
	if CommitVisual(b, view, marks, keyFn) {
		return
	}
	i := b.Index()
	if i < 0 || i >= len(view) {
		return
	}
	k := keyFn(view[i])
	if k == "" {
		return
	}
	if _, ok := marks[k]; ok {
		delete(marks, k)
		return
	}
	marks[k] = struct{}{}
}

// CancelVisualOnLostAnchor drops a range whose anchor row left the
// view and names the reason. A preview hanging off a row the user can
// no longer see is a trap: the next `Space` would mark a range nobody
// chose. Pages call it after every message that rebuilds the view.
func CancelVisualOnLostAnchor[E any](b *Base, view []E, keyFn func(E) string) tea.Cmd {
	if !b.Visual.on || indexOfKey(view, b.Visual.anchor, keyFn) >= 0 {
		return nil
	}
	b.Visual.Cancel()
	return footer.ShowFlash(footer.FlashInfo, HintVisualCancelled)
}

// indexOfKey resolves a row key against one frame's view, or -1 when
// the row is gone — the anchor's only link to a position.
func indexOfKey[E any](view []E, key string, keyFn func(E) string) int {
	for i, e := range view {
		if keyFn(e) == key {
			return i
		}
	}
	return -1
}

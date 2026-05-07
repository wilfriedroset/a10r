// SPDX-License-Identifier: Apache-2.0

package cursor

// SortKeyHandler is the small surface cursor.HandleSort needs from a
// page's sort engine — anything with a HandleKey(key) bool method
// satisfies it. tablesort.Sorter[T] is the concrete consumer; the
// interface lives here so this package doesn't import tablesort.
type SortKeyHandler interface {
	HandleKey(key string) bool
}

// HandleSort routes a key through the page's sorter and, on a sort
// state change, runs the page's clearFocus + recompute hooks.
// Returns true when the key was consumed.
//
// User-initiated re-sort is k9s-positional: the cursor stays at the
// same row index, whichever entry lands under it becomes the new
// focus. Clearing the page's focus field (whichever it tracks) runs
// before recompute so the find-by-key branch in recompute is
// bypassed and the cursor stays index-stable for this one call.
// snapshotFocus inside recompute (or right after, depending on the
// page) re-captures the new focus content-stably for subsequent
// poll / scope / filter recomputes.
//
// The tenant page is the documented exception — it carries no
// focus field, so its handleSort just calls sorter.HandleKey
// directly without going through this helper.
func HandleSort(key string, sorter SortKeyHandler, clearFocus, recompute func()) bool {
	if !sorter.HandleKey(key) {
		return false
	}
	clearFocus()
	recompute()
	return true
}

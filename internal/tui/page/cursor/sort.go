// SPDX-License-Identifier: Apache-2.0

package cursor

// SortKeyHandler is the surface HandleSort needs from a page's sort
// engine (tablesort.Sorter[T] satisfies it). Declared here so this
// package doesn't import tablesort.
type SortKeyHandler interface {
	HandleKey(key string) bool
}

// HandleSort routes a key through the page's sorter and, on a sort
// state change, runs clearFocus then recompute. Returns true when the
// key was consumed.
//
// Re-sort is k9s-positional: the cursor stays at the same row index,
// whatever lands under it becomes the new focus. clearFocus must run
// before recompute so recompute's find-by-key branch is bypassed and
// the cursor stays index-stable for this call.
func HandleSort(key string, sorter SortKeyHandler, clearFocus, recompute func()) bool {
	if !sorter.HandleKey(key) {
		return false
	}
	clearFocus()
	recompute()
	return true
}

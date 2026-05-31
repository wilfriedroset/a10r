// SPDX-License-Identifier: Apache-2.0

// Package cursor holds the cursor / scroll primitives shared across
// table pages (which embed Window) and 1D scroll viewers (which call
// HalfPageStep / FullPageStep against their own scalar scroll field).
package cursor

// HalfPageStep returns the Ctrl+D / Ctrl+U distance: half the body
// height, with a floor for the unsized-page case.
func HalfPageStep(bodyHeight int) int {
	if bodyHeight < 2 {
		return 10
	}
	return max(bodyHeight/2, 1)
}

// FullPageStep returns the Ctrl+F / Ctrl+B distance: body height minus
// two for header / footer overlap, with the same floor as HalfPageStep.
func FullPageStep(bodyHeight int) int {
	if bodyHeight < 4 {
		return 20
	}
	return max(bodyHeight-2, 1)
}

// SPDX-License-Identifier: Apache-2.0

// Package cursor holds the cursor / scroll primitives shared across
// every page that renders a row table or a scrolling viewer. Table
// pages embed Window for the bundled cursor / topRow / bodyHeight
// triple; 1D scroll viewers (alert, silence, status, tenantconfig,
// help) call HalfPageStep / FullPageStep directly against their own
// scalar scroll field.
package cursor

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

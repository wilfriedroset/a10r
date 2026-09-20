// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// PickerOriginSkin tags the `:skin` picker so its submission folds
// into the live styles instead of reaching the page underneath.
const PickerOriginSkin = "skin"

// currentSkinMarker labels the applied skin in the picker. It is
// stripped from the submitted selection, so it must not be a suffix
// any skin name can carry: theme.validSkinName forbids spaces and
// parens, which is what makes the strip unambiguous.
const currentSkinMarker = " (current)"

// ApplySkinMsg switches the live skin. The change is session-local:
// it never writes theme.name, so the next start reads the config
// file as before.
type ApplySkinMsg struct {
	Name string
}

// OpenSkinPickerMsg opens the picker over every resolvable skin. The
// App builds the picker rather than the `:` wiring, because only the
// App knows which skin is applied right now.
type OpenSkinPickerMsg struct{}

// ApplySkin returns a Cmd that switches the live skin by name.
func ApplySkin(name string) tea.Cmd {
	return func() tea.Msg { return ApplySkinMsg{Name: name} }
}

// OpenSkinPicker returns a Cmd that opens the skin picker.
func OpenSkinPicker() tea.Cmd {
	return func() tea.Msg { return OpenSkinPickerMsg{} }
}

// applySkin compiles the named skin and, only on success, copies it
// into the shared *theme.Styles every page and chrome component
// holds. One assignment restyles the whole tree; a failure leaves the
// working skin in place, because half-applied styles render worse
// than no change at all.
//
// An unknown name is refused rather than resolved. The loader's
// fallback to the default skin exists for a config file read at
// startup; here the user typed a specific name, and silently
// repainting to something else reads as a10r ignoring them.
func (a *App) applySkin(name string) tea.Cmd {
	if a.loadStyles == nil || a.skinNames == nil {
		return showFlash(footer.FlashWarn, "skin switching is unavailable")
	}
	if !slices.Contains(a.skinNames(), name) {
		return showFlash(footer.FlashWarn, fmt.Sprintf("skin %q not found", name))
	}
	styles, err := a.loadStyles(name)
	if err != nil {
		return showFlash(footer.FlashWarn, fmt.Sprintf("skin %q: %v", name, err))
	}
	*a.styles = *styles
	a.skinName = name
	// The background query is asynchronous, so a skin picked before
	// the terminal answers would otherwise be overwritten a frame
	// later by auto-detection.
	a.autoTheme = false
	return nil
}

// openSkinPicker lists every resolvable skin with the applied one
// marked, in single-select mode: a skin is a choice of one.
func (a *App) openSkinPicker() tea.Cmd {
	// Both guards, not just skinNames: a picker that lists skins and
	// then refuses every selection is a worse answer than refusing to
	// open.
	if a.loadStyles == nil || a.skinNames == nil {
		return showFlash(footer.FlashWarn, "skin switching is unavailable")
	}
	names := a.skinNames()
	items := make([]string, len(names))
	for i, name := range names {
		items[i] = name
		if name == a.skinName {
			items[i] += currentSkinMarker
		}
	}
	return OpenModal(func() modal.Modal {
		return modal.NewPicker("skins", items, modal.PickerSingle).
			WithOrigin(PickerOriginSkin)
	})
}

// skinFromSelection strips the current-skin marker the picker adds,
// so the label the user saw resolves back to a loadable name.
func skinFromSelection(selections []string) string {
	if len(selections) == 0 {
		return ""
	}
	return strings.TrimSuffix(selections[0], currentSkinMarker)
}

// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// newSkinApp wires an App that can switch skins, over a private
// Styles pointer for the same reason newAutoThemeApp uses one: the
// swap writes through it.
func newSkinApp(t *testing.T, load func(string) (*theme.Styles, error)) (*App, *theme.Styles) {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		LoadStyles: load,
		SkinNames:  func() []string { return []string{"nord", theme.DefaultSkinName} },
		SkinName:   theme.DefaultSkinName,
		Session:    testutil.Session(),
	})
	return a, styles
}

// The skin has to land in the struct every page and chrome component
// already holds, not in a new one, or the switch shows up on nothing
// until the page stack turns over.
func TestApp_ApplySkinWritesThroughTheSharedStyles(t *testing.T) {
	t.Parallel()

	a, styles := newSkinApp(t, func(string) (*theme.Styles, error) { return markerStyles(), nil })

	_, cmd := a.Update(ApplySkinMsg{Name: "nord"})

	require.Nil(t, cmd, "a skin that applied cleanly must not flash")
	require.Equal(t, markerStyles().Body.Default, styles.Body.Default)
	require.Equal(t, "nord", a.skinName)
}

// The user typed a specific name, so the startup fallback to the
// default skin must not apply: a typo that silently repaints the UI
// reads as "a10r ignored me".
func TestApp_ApplySkinRejectsAnUnknownName(t *testing.T) {
	t.Parallel()

	var loaded bool
	a, styles := newSkinApp(t, func(string) (*theme.Styles, error) {
		loaded = true
		return markerStyles(), nil
	})
	before := styles.Body.Default

	_, cmd := a.Update(ApplySkinMsg{Name: "nope"})

	require.False(t, loaded, "an unknown name must not reach the loader")
	require.Equal(t, before, styles.Body.Default)
	require.Equal(t, footer.FlashShowMsg{Level: footer.FlashWarn, Text: `skin "nope" not found`}, cmd())
}

// A skin that parses but does not compile must leave the UI on the
// one that works. Half-applied styles render worse than no change.
func TestApp_ApplySkinKeepsTheStylesOnACompileError(t *testing.T) {
	t.Parallel()

	a, styles := newSkinApp(t, func(string) (*theme.Styles, error) {
		return nil, errors.New("body.fgColor is required")
	})
	before := styles.Body.Default

	_, cmd := a.Update(ApplySkinMsg{Name: "nord"})

	require.Equal(t, before, styles.Body.Default)
	require.Equal(t, theme.DefaultSkinName, a.skinName)

	msg, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "body.fgColor is required")
}

// The background query is asynchronous, so a user who picks a skin
// before the terminal answers would otherwise have it overwritten by
// auto-detection a frame later.
func TestApp_ApplySkinDisarmsAutoDetection(t *testing.T) {
	t.Parallel()

	a, _ := newSkinApp(t, func(string) (*theme.Styles, error) { return markerStyles(), nil })
	a.autoTheme = true

	a.Update(ApplySkinMsg{Name: "nord"})

	require.False(t, a.autoTheme)
}

// The picker is built by the App rather than by the cmdbar wiring
// because only the App knows which skin is applied right now, and an
// unmarked list cannot answer "what am I looking at".
func TestApp_SkinPickerMarksTheCurrentSkin(t *testing.T) {
	t.Parallel()

	a, _ := newSkinApp(t, func(string) (*theme.Styles, error) { return markerStyles(), nil })

	_, cmd := a.Update(OpenSkinPickerMsg{})
	require.NotNil(t, cmd)
	a.Update(cmd())

	frame := a.overlays.modal.View(80, 20)
	require.Contains(t, frame, theme.DefaultSkinName+" (current)")
	require.Contains(t, frame, "nord")
}

// A submission carries the marked label, so the App has to strip the
// marker before it reaches the loader.
func TestApp_SkinPickerSubmissionAppliesTheNamedSkin(t *testing.T) {
	t.Parallel()

	var got string
	a, _ := newSkinApp(t, func(name string) (*theme.Styles, error) {
		got = name
		return markerStyles(), nil
	})

	_, cmd := a.Update(modal.PickerSubmittedMsg{
		Origin:     PickerOriginSkin,
		Selections: []string{theme.DefaultSkinName + " (current)"},
	})
	require.NotNil(t, cmd)
	a.Update(cmd())

	require.Equal(t, theme.DefaultSkinName, got)
}

// Esc on the skin picker keeps the current skin, the same way Esc on
// the tenant picker keeps the current scope.
func TestApp_SkinPickerCancelChangesNothing(t *testing.T) {
	t.Parallel()

	a, styles := newSkinApp(t, func(string) (*theme.Styles, error) { return markerStyles(), nil })
	before := styles.Body.Default

	_, cmd := a.Update(modal.PickerCancelledMsg{Origin: PickerOriginSkin})

	require.Nil(t, cmd)
	require.Equal(t, before, styles.Body.Default)
}

// A picker that lists skins and then refuses every selection is a
// worse answer than no picker: the user reads the list as an offer.
func TestApp_OpenSkinPickerRefusesWithoutALoader(t *testing.T) {
	t.Parallel()

	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		SkinNames:  func() []string { return []string{"nord"} },
		SkinName:   theme.DefaultSkinName,
		Session:    testutil.Session(),
	})

	_, cmd := a.Update(OpenSkinPickerMsg{})

	require.NotNil(t, cmd)
	require.Equal(t, footer.FlashShowMsg{Level: footer.FlashWarn, Text: "skin switching is unavailable"}, cmd())
}

// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/keys"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// markerStyles builds a Styles whose body foreground is a colour no
// bundled skin uses, so a test can tell "the swap landed" from "the
// provisional skin is still in place" without comparing whole skins.
func markerStyles() *theme.Styles {
	return &theme.Styles{
		Body: theme.BodyStyle{Default: lipgloss.NewStyle().Foreground(lipgloss.Color("#ff00ff"))},
	}
}

// newAutoThemeApp wires an App in auto mode over a private Styles
// pointer. The pointer must not come from testutil.LoadStyles: that
// one is cached and shared across the whole test binary, and the
// theme swap writes through it.
func newAutoThemeApp(t *testing.T, load func(string) (*theme.Styles, error)) (*App, *theme.Styles) {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		AutoTheme:  true,
		LoadStyles: load,
		Session:    testutil.Session(),
	})
	return a, styles
}

func TestApp_InitRequestsBackgroundColorInAutoMode(t *testing.T) {
	t.Parallel()

	a, _ := newAutoThemeApp(t, func(string) (*theme.Styles, error) { return markerStyles(), nil })
	cmd := a.Init()
	require.NotNil(t, cmd,
		"auto mode must ask the terminal for its background colour at startup")

	// Tips are off in the fixture, so the background query is the only
	// startup command and Batch hands it back unwrapped. Comparing the
	// emitted message pins which query it is, not merely that one exists.
	require.Equal(t, tea.RequestBackgroundColor(), cmd())
}

func TestApp_InitSkipsBackgroundQueryWithExplicitTheme(t *testing.T) {
	t.Parallel()

	// An explicit theme is the user's decision. Querying anyway would
	// be a wasted round trip and risks overriding that decision.
	a := newTestApp(t)
	require.Nil(t, a.Init())
}

func TestApp_AutoThemeSwapsStylesInPlace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		bg       color.Color
		wantSkin string
	}{
		{name: "dark terminal keeps the dark skin", bg: lipgloss.Color("#000000"), wantSkin: theme.DefaultSkinName},
		{name: "light terminal switches to the light skin", bg: lipgloss.Color("#ffffff"), wantSkin: theme.LightSkinName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var asked []string
			a, styles := newAutoThemeApp(t, func(name string) (*theme.Styles, error) {
				asked = append(asked, name)
				return markerStyles(), nil
			})

			_, cmd := a.Update(tea.BackgroundColorMsg{Color: tc.bg})
			require.Nil(t, cmd)
			require.Equal(t, []string{tc.wantSkin}, asked)

			// Every page holds this same pointer, so the in-place write
			// is what restyles the whole tree in one step.
			require.Equal(t, markerStyles().Body.Default, styles.Body.Default,
				"the swap must write through the shared Styles pointer")
		})
	}
}

func TestApp_AutoThemeDetectsOnce(t *testing.T) {
	t.Parallel()

	// Terminals re-report their background on their own schedule. A
	// second swap would undo an in-session skin change, so detection
	// is a one-shot.
	var calls int
	a, _ := newAutoThemeApp(t, func(string) (*theme.Styles, error) {
		calls++
		return markerStyles(), nil
	})

	a.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	a.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#000000")})
	require.Equal(t, 1, calls)
}

func TestApp_AutoThemeKeepsProvisionalSkinOnLoadError(t *testing.T) {
	t.Parallel()

	// A failed detection is not worth a crash at startup: the
	// provisional skin is already readable, just possibly the wrong
	// polarity.
	a, styles := newAutoThemeApp(t, func(string) (*theme.Styles, error) {
		return nil, errors.New("boom")
	})
	before := styles.Body.Default

	_, cmd := a.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	require.Nil(t, cmd)
	require.Equal(t, before, styles.Body.Default)
}

func TestApp_BackgroundColorIgnoredWithExplicitTheme(t *testing.T) {
	t.Parallel()

	called := false
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	a := NewApp(Options{
		Styles:     styles,
		Dispatcher: keys.New(nil),
		LoadStyles: func(string) (*theme.Styles, error) { called = true; return markerStyles(), nil },
		Session:    testutil.Session(),
	})

	a.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	require.False(t, called, "an explicit theme must survive a terminal background report")
}

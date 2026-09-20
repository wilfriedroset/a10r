// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
	"github.com/wilfriedroset/a10r/internal/uistate"
)

func TestAutoSentinelSpelledOnceAcrossLayers(t *testing.T) {
	t.Parallel()

	// config cannot import internal/tui/theme, so the sentinel is
	// spelled in both packages. boot is the only package that sees
	// both, which makes this the place the two cannot drift apart.
	require.Equal(t, config.ThemeAuto, theme.AutoSkinName)
}

func TestIsAutoTheme(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "sentinel", in: theme.AutoSkinName, want: true},
		{name: "empty resolves to the default, which is the sentinel", in: "", want: true},
		{name: "named bundled skin", in: theme.DefaultSkinName},
		{name: "named user skin", in: "gruvbox-dark"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isAutoTheme(tc.in))
		})
	}
}

func TestStartupSkinName(t *testing.T) {
	t.Parallel()

	// The picker marks this name as current, so it must be a name the
	// loader can resolve rather than the sentinel the first frame was
	// asked for.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "sentinel resolves to the provisional dark skin", in: theme.AutoSkinName, want: theme.DefaultSkinName},
		{name: "unset resolves the same way", in: "", want: theme.DefaultSkinName},
		{name: "named skin is passed through", in: theme.LightSkinName, want: theme.LightSkinName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, startupSkinName(tc.in))
		})
	}
}

func TestDefaultLoadStyles_AutoLoadsTheDarkSkinProvisionally(t *testing.T) {
	t.Parallel()

	// The sentinel never names a file, so boot must resolve it to a
	// real skin before the App has a terminal background to go on.
	auto, err := defaultLoadStyles(theme.AutoSkinName, t.TempDir())
	require.NoError(t, err)

	dark, err := defaultLoadStyles(theme.DefaultSkinName, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, dark.Body.Default, auto.Body.Default)
}

func TestBuildApp_ArmsAutoThemeFromConfig(t *testing.T) {
	t.Parallel()

	// The seam where "config says auto" becomes "App is armed": both
	// ends are unit-tested, so only this wiring can silently drop the
	// feature. Tips are off in the fixture, so Init's only startup
	// command is the terminal background query.
	cases := []struct {
		name      string
		themeName string
		wantArmed bool
	}{
		{name: "sentinel arms detection", themeName: theme.AutoSkinName, wantArmed: true},
		{name: "unset arms detection", themeName: "", wantArmed: true},
		{name: "named skin is left alone", themeName: theme.LightSkinName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
			require.NoError(t, err)
			cfg := &config.Config{Theme: config.Theme{Name: tc.themeName}}

			a := buildApp(buildDispatcher(), nil, styles, cfg, &pollerRegistry{}, testDeps(t).resolved(), t.TempDir(), scopeAll, uistate.Open(""))
			if tc.wantArmed {
				require.NotNil(t, a.Init())
				return
			}
			require.Nil(t, a.Init())
		})
	}
}

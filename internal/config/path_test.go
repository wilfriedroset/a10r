// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/xdg"
)

// Test fixture roots are promoted to consts so the gocritic
// `filepathJoin` rule (which only inspects inline literals) does not
// flag the simulated absolute paths the test deliberately uses.
const (
	fakeHome      = "/home/user"
	fakeXDGCfg    = "/srv/cfg"
	fakeLocalData = `C:\Users\u\AppData\Local`
	fakeExplicit  = "/etc/a10r"
)

func TestDefaultConfigDirFor(t *testing.T) {
	t.Parallel()

	homeOK := func() (string, error) { return fakeHome, nil }
	homeErr := func() (string, error) { return "", errors.New("home unset") }

	envEmpty := func(string) string { return "" }
	envXDG := func(k string) string {
		if k == xdg.ConfigHome {
			return fakeXDGCfg
		}
		return ""
	}
	envLocal := func(k string) string {
		if k == xdg.LocalAppData {
			return fakeLocalData
		}
		return ""
	}

	cases := []struct {
		name    string
		goos    string
		env     func(string) string
		homeDir func() (string, error)
		want    string
		wantErr bool
	}{
		{
			name:    "linux without XDG falls back to ~/.config",
			goos:    "linux",
			env:     envEmpty,
			homeDir: homeOK,
			want:    filepath.Join(fakeHome, ".config", "a10r"),
		},
		{
			name:    "linux with XDG_CONFIG_HOME wins",
			goos:    "linux",
			env:     envXDG,
			homeDir: homeOK,
			want:    filepath.Join(fakeXDGCfg, "a10r"),
		},
		{
			name:    "linux home error surfaces when no XDG",
			goos:    "linux",
			env:     envEmpty,
			homeDir: homeErr,
			wantErr: true,
		},
		{
			name:    "darwin uses Library/Application Support",
			goos:    "darwin",
			env:     envEmpty,
			homeDir: homeOK,
			want:    filepath.Join(fakeHome, "Library", "Application Support", "a10r"),
		},
		{
			name:    "darwin home error surfaces",
			goos:    "darwin",
			env:     envEmpty,
			homeDir: homeErr,
			wantErr: true,
		},
		{
			name:    "windows uses LOCALAPPDATA",
			goos:    "windows",
			env:     envLocal,
			homeDir: homeOK,
			want:    filepath.Join(fakeLocalData, "a10r"),
		},
		{
			name:    "windows without LOCALAPPDATA errors",
			goos:    "windows",
			env:     envEmpty,
			homeDir: homeOK,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := defaultConfigDirFor(tc.goos, tc.env, tc.homeDir)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveConfigDir_PrecedenceOrder(t *testing.T) {
	t.Parallel()

	homeOK := func() (string, error) { return fakeHome, nil }

	cases := []struct {
		name     string
		explicit string
		env      func(string) string
		want     string
	}{
		{
			name:     "explicit wins over env and default",
			explicit: fakeExplicit,
			env: func(k string) string {
				if k == envConfigDir {
					return fakeXDGCfg
				}
				return ""
			},
			want: fakeExplicit,
		},
		{
			name: "env wins when explicit is empty",
			env: func(k string) string {
				if k == envConfigDir {
					return fakeXDGCfg
				}
				return ""
			},
			want: fakeXDGCfg,
		},
		{
			name: "default kicks in when explicit and env both empty",
			env:  func(string) string { return "" },
			want: filepath.Join(fakeHome, ".config", "a10r"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveConfigDir(tc.explicit, tc.env, homeOK, "linux")
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDefaultDir_HostOSSucceeds(t *testing.T) {
	t.Parallel()

	// Sanity: on the host CI runs on, DefaultDir must succeed and
	// produce a path containing "a10r" — confirms the public wrapper
	// is wired up.
	dir, err := DefaultDir()
	require.NoError(t, err)
	require.Contains(t, dir, "a10r")
	require.True(t, filepath.IsAbs(dir), "default dir must be absolute, got %q", dir)
}

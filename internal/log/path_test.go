// SPDX-License-Identifier: Apache-2.0

package log

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
	fakeXDGState  = "/srv/state"
	fakeLocalData = `C:\Users\u\AppData\Local`
)

func TestDefaultPathFor(t *testing.T) {
	t.Parallel()

	homeOK := func() (string, error) { return fakeHome, nil }
	homeErr := func() (string, error) { return "", errors.New("home unset") }

	envEmpty := func(string) string { return "" }
	envXDGStateHome := func(k string) string {
		if k == xdg.StateHome {
			return fakeXDGState
		}
		return ""
	}
	envLocalAppData := func(k string) string {
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
			name:    "linux without XDG falls back to home",
			goos:    "linux",
			env:     envEmpty,
			homeDir: homeOK,
			want:    filepath.Join(fakeHome, ".local", "state", "a10r", "a10r.log"),
		},
		{
			name:    "linux with XDG_STATE_HOME wins",
			goos:    "linux",
			env:     envXDGStateHome,
			homeDir: homeOK,
			want:    filepath.Join(fakeXDGState, "a10r", "a10r.log"),
		},
		{
			name:    "linux home error surfaces when no XDG",
			goos:    "linux",
			env:     envEmpty,
			homeDir: homeErr,
			wantErr: true,
		},
		{
			name:    "freebsd uses unix path (XDG default branch)",
			goos:    "freebsd",
			env:     envEmpty,
			homeDir: homeOK,
			want:    filepath.Join(fakeHome, ".local", "state", "a10r", "a10r.log"),
		},
		{
			name:    "darwin uses Library/Logs",
			goos:    "darwin",
			env:     envEmpty,
			homeDir: homeOK,
			want:    filepath.Join(fakeHome, "Library", "Logs", "a10r", "a10r.log"),
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
			env:     envLocalAppData,
			homeDir: homeOK,
			want:    filepath.Join(fakeLocalData, "a10r", "Logs", "a10r.log"),
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

			got, err := defaultPathFor(tc.goos, tc.env, tc.homeDir)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDefaultPath_HostOSReturnsNonEmpty(t *testing.T) {
	t.Parallel()

	// Sanity: on the host CI runs on, DefaultPath must succeed and
	// produce a path containing "a10r" — confirms the public wrapper
	// is wired up.
	path, err := DefaultPath()
	require.NoError(t, err)
	require.Contains(t, path, "a10r")
	require.True(t, filepath.IsAbs(path), "default path must be absolute, got %q", path)
}

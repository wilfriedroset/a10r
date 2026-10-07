// SPDX-License-Identifier: Apache-2.0

package xdg_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/xdg"
)

// envMap returns a stable env-lookup func over the supplied map so
// tests drive every StateDir branch without setenv contamination.
func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestStateDir(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("no home")
	stateRoot := filepath.Join(string(filepath.Separator), "state")
	homeRoot := filepath.Join(string(filepath.Separator), "home", "u")
	okHome := func() (string, error) { return homeRoot, nil }

	tests := []struct {
		name    string
		env     map[string]string
		homeDir func() (string, error)
		want    string
		wantErr error
	}{
		{
			name:    "state home wins",
			env:     map[string]string{xdg.StateHome: stateRoot},
			homeDir: okHome,
			want:    filepath.Join(stateRoot, "a10r"),
		},
		{
			name:    "falls back to home",
			homeDir: okHome,
			want:    filepath.Join(homeRoot, ".local", "state", "a10r"),
		},
		{
			name:    "home error propagates",
			homeDir: func() (string, error) { return "", sentinel },
			wantErr: sentinel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := xdg.StateDir(envMap(tt.env), tt.homeDir)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultStateDir(t *testing.T) {
	t.Parallel()

	got, err := xdg.DefaultStateDir()
	require.NoError(t, err)
	require.Equal(t, "a10r", filepath.Base(got))
}

func TestAtomicWrite_CreatesOwnerOnlyFileAndDir(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "ring")

	require.NoError(t, xdg.AtomicWrite(path, []byte("one\n")))

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm(),
		"state dir must be owner-only — it can leak recent queries on a shared host")

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"state files must be owner-only for the same reason")

	require.NoError(t, xdg.AtomicWrite(path, []byte("two\n")))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "two\n", string(got), "second write replaces the content")

	requireNoTemp(t, path)
}

func TestAtomicWrite_FailedRenameLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sibling := filepath.Join(dir, "sibling")
	require.NoError(t, xdg.AtomicWrite(sibling, []byte("keep\n")))

	// A directory at the destination makes os.Rename fail after the
	// temp file exists — the post-temp cleanup path.
	path := filepath.Join(dir, "blocked")
	require.NoError(t, os.Mkdir(path, xdg.DirMode))

	require.Error(t, xdg.AtomicWrite(path, []byte("nope\n")))

	got, err := os.ReadFile(sibling)
	require.NoError(t, err)
	require.Equal(t, "keep\n", string(got), "a failed write must not touch other state")
	requireNoTemp(t, path)
}

// TestAtomicWrite_ReplacesUnwritableDestination pins the rename
// itself: a direct O_TRUNC write to a 0o400 destination fails with
// EACCES, so this only passes while the swap goes through a temp file.
func TestAtomicWrite_ReplacesUnwritableDestination(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "readonly")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o400))

	require.NoError(t, xdg.AtomicWrite(path, []byte("new\n")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new\n", string(got))
	requireNoTemp(t, path)
}

func requireNoTemp(t *testing.T, path string) {
	t.Helper()

	leftovers, err := filepath.Glob(path + ".*.tmp")
	require.NoError(t, err)
	require.Empty(t, leftovers, "no temp file may survive an AtomicWrite")
}

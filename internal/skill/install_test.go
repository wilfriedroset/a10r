// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContent_IsEmbeddedSkill(t *testing.T) {
	t.Parallel()

	got := Content()
	require.NotEmpty(t, got)
	require.Contains(t, string(got), "name: a10r",
		"Content must return the embedded SKILL.md, frontmatter and all")
}

func TestResolveDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		claude  bool
		dest    string
		want    string
		wantErr bool
	}{
		{name: "default is vendor-neutral agents dir", want: filepath.Join(home, ".agents", "skills")},
		{name: "claude targets claude dir", claude: true, want: filepath.Join(home, ".claude", "skills")},
		{name: "dest overrides", dest: "/tmp/custom", want: "/tmp/custom"},
		{name: "claude and dest are mutually exclusive", claude: true, dest: "/tmp/custom", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDir(tc.claude, tc.dest)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestInstall_WritesSkillWithLockedDownPerms(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := Install(dir, false)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "a10r", "SKILL.md"), path)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, Content(), got, "installed bytes must equal the embedded skill")

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())

	di, err := os.Stat(filepath.Join(dir, "a10r"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o750), di.Mode().Perm())

	leftovers, err := filepath.Glob(filepath.Join(dir, "a10r", ".SKILL-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, leftovers, "atomic write must leave no temp file behind")
}

func TestInstall_RefusesClobberWithoutForce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := Install(dir, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("user edit"), 0o640))

	_, err = Install(dir, false)
	require.Error(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "user edit", string(got), "a refused install must not touch the file")
}

func TestInstall_ForceOverwrites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := Install(dir, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("stale"), 0o640))

	_, err = Install(dir, true)
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, Content(), got)
}

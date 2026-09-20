// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// The picker offers what the loader can resolve, so the two lists
// have to come from the same rules: bundled plus user files, sorted,
// and a user file that shadows a bundled name listed once.
func TestNames_UnionOfBundledAndUser(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"nord.yaml", "catppuccin-mocha.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600))
	}

	got := Names(dir)

	require.Contains(t, got, "nord")
	require.Contains(t, got, DefaultSkinName)
	require.Contains(t, got, LightSkinName)
	require.Equal(t, 1, countOf(got, DefaultSkinName), "a shadowing user file must not duplicate the name")
	require.IsIncreasing(t, got)
	require.NotContains(t, got, AutoSkinName, "the sentinel is not a skin the picker can apply")
}

// A host with no skins directory is the common case, not an error:
// the picker still has to offer the bundled set.
func TestNames_MissingUserDirStillListsTheBundledSet(t *testing.T) {
	t.Parallel()

	require.Equal(t, Names(""), Names(filepath.Join(t.TempDir(), "absent")))
	require.Contains(t, Names(""), DefaultSkinName)
}

// The loader rejects names outside validSkinName before it touches
// the filesystem, so offering one in the picker would hand the user
// an entry that cannot be applied.
func TestNames_SkipsFilesTheLoaderWouldRefuse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not a skin.yaml"), []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("hi\n"), 0o600))

	got := Names(dir)

	require.NotContains(t, got, "not a skin")
	require.NotContains(t, got, "readme")
}

func countOf(names []string, want string) int {
	n := 0
	for _, name := range names {
		if name == want {
			n++
		}
	}
	return n
}

// The sentinel is reserved: Load refuses it and defaultLoadStyles maps
// it to the default skin before Load ever sees it. Offering `auto` from
// the user directory would therefore repaint to the default while the
// picker went on marking `auto` as current.
func TestNames_SkipsTheAutoSentinelFromTheUserDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, AutoSkinName+".yaml"), []byte("{}\n"), 0o600))

	require.NotContains(t, Names(dir), AutoSkinName)
}

// The picker's bundled half is read from the embed.FS, while
// bundledNames is spelled out from SOURCES.yaml. A skin added to one
// and not the other is otherwise invisible until a user goes looking
// for it in the picker.
func TestNames_BundledHalfMatchesSourcesYAML(t *testing.T) {
	t.Parallel()

	want := slices.Clone(bundledNames)
	slices.Sort(want)
	require.Equal(t, want, Names(""))
}

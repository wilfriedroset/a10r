// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBundledNames_ReturnsAllSkins(t *testing.T) {
	t.Parallel()

	// Pin the bundled skin set: the four catppuccin variants
	// (mocha / latte / frappe / macchiato) plus gruvbox-dark as a
	// non-catppuccin alternative. A refactor that drops or moves a
	// file fails this test rather than silently shipping a binary
	// with fewer skins.
	names, err := BundledNames()
	require.NoError(t, err)
	require.Len(t, names, 5)
	for _, want := range []string{
		"catppuccin-mocha", "catppuccin-latte",
		"catppuccin-frappe", "catppuccin-macchiato",
		"gruvbox-dark",
	} {
		require.Contains(t, names, want)
	}
}

func TestLoad_EachBundledSkinCompiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"catppuccin-mocha", "catppuccin-latte",
		"catppuccin-frappe", "catppuccin-macchiato",
		"gruvbox-dark",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			styles, err := (&Loader{}).Load(name)
			require.NoError(t, err)
			require.NotNil(t, styles)

			// Spot-check one role per major group so a future role
			// rename in the schema fails this test rather than
			// silently ending up with a zero-value lipgloss.Style.
			require.NotEmpty(t, styles.Body.Default.GetForeground())
			require.NotEmpty(t, styles.Header.Accent.GetForeground())
			require.NotEmpty(t, styles.Table.Cursor.GetBackground())
			require.NotEmpty(t, styles.Severity.Critical.GetForeground())
			require.NotEmpty(t, styles.SilenceState.Active.GetForeground())
			require.NotEmpty(t, styles.Flash.Error.GetForeground())
		})
	}
}

func TestLoad_EmptyNameUsesDefault(t *testing.T) {
	t.Parallel()

	styles, err := (&Loader{}).Load("")
	require.NoError(t, err)
	require.NotNil(t, styles)
}

func TestLoad_UnknownNameFallsBackToDefault(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	styles, err := (&Loader{Logger: logger}).Load("does-not-exist")
	require.NoError(t, err)
	require.NotNil(t, styles)

	out := buf.String()
	require.Contains(t, out, "unknown skin")
	require.Contains(t, out, `requested=does-not-exist`)
	require.Contains(t, out, `default=catppuccin-mocha`)
}

func TestLoad_UserSkinShadowsBundledLogsWarning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Copy the bundled mocha file into the user dir so it shadows.
	raw, err := readBundled("catppuccin-mocha")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "catppuccin-mocha.yaml"), raw, 0o600))

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	styles, err := (&Loader{UserDir: dir, Logger: logger}).Load("catppuccin-mocha")
	require.NoError(t, err)
	require.NotNil(t, styles)

	require.Contains(t, buf.String(), "user skin shadows bundled skin",
		"shadow warning must fire when user file matches a bundled name")
}

func TestLoad_UserSkinUniqueNameDoesNotWarn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw, err := readBundled("catppuccin-mocha")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "my-custom.yaml"), raw, 0o600))

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	_, err = (&Loader{UserDir: dir, Logger: logger}).Load("my-custom")
	require.NoError(t, err)

	require.NotContains(t, buf.String(), "shadows bundled",
		"shadow warning must NOT fire for a name that doesn't ship bundled")
}

func TestLoad_RejectsUndefinedPaletteRef(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "undefined_palette_ref.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "undefined.yaml"), raw, 0o600))

	_, err = (&Loader{UserDir: dir}).Load("undefined")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSkin)
	require.Contains(t, err.Error(), "nonexistent",
		"error must name the missing palette key")
}

func TestLoad_RejectsBadHex(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "bad_hex.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "badhex.yaml"), raw, 0o600))

	_, err = (&Loader{UserDir: dir}).Load("badhex")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSkin)
	require.Contains(t, err.Error(), "hex",
		"error must explain why the colour was rejected")
}

func TestLoad_RejectsMissingRole(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "missing_role.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "missingrole.yaml"), raw, 0o600))

	_, err = (&Loader{UserDir: dir}).Load("missingrole")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidSkin)
}

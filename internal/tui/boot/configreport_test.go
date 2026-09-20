// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
}

// config.Load only knows the base file and its drop-ins. The three
// overlays a start also reads live outside the loader, so the page
// has to name them itself or the report answers half the question.
func TestBuildConfigReport_NamesTheOverlaysTheLoaderNeverSees(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := filepath.Join(dir, "a10r.yaml")
	writeFile(t, base)
	writeFile(t, filepath.Join(dir, config.AliasesFile))
	writeFile(t, filepath.Join(dir, config.KeysDir, config.DefaultKeysProfile+".yaml"))
	writeFile(t, filepath.Join(dir, theme.SkinsDir, "nord.yaml"))

	in := buildConfigReport(configInputs{
		cfg: &config.Config{
			Sources: []config.Source{{Kind: config.SourceBase, Path: base}},
			Theme:   config.Theme{Name: "nord"},
		},
		configDir: dir,
		capture:   &a10rlog.Capture{},
	})()

	require.Equal(t, []config.Source{
		{Kind: config.SourceBase, Path: base},
		{Kind: config.SourceAliases, Path: filepath.Join(dir, config.AliasesFile)},
		{Kind: config.SourceKeys, Path: filepath.Join(dir, config.KeysDir, config.DefaultKeysProfile+".yaml")},
		{Kind: config.SourceSkin, Path: filepath.Join(dir, theme.SkinsDir, "nord.yaml")},
	}, in.Sources)
}

// An operator who curates none of the overlays must not read three
// lines naming files that do not exist.
func TestBuildConfigReport_SkipsTheOverlaysThatAreAbsent(t *testing.T) {
	t.Parallel()

	in := buildConfigReport(configInputs{
		cfg:       &config.Config{},
		configDir: t.TempDir(),
		capture:   &a10rlog.Capture{},
	})()

	require.Empty(t, in.Sources)
}

// The auto sentinel is not a filename. The skin it resolves to is the
// one a user file can shadow, so that is the path to stat.
func TestBuildConfigReport_ResolvesTheAutoSkinToItsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, theme.SkinsDir, theme.DefaultSkinName+".yaml"))

	in := buildConfigReport(configInputs{
		cfg:       &config.Config{Theme: config.Theme{Name: theme.AutoSkinName}},
		configDir: dir,
		capture:   &a10rlog.Capture{},
	})()

	require.Equal(t, []config.Source{
		{Kind: config.SourceSkin, Path: filepath.Join(dir, theme.SkinsDir, theme.DefaultSkinName+".yaml")},
	}, in.Sources)
}

// Under the auto sentinel the App re-loads through AutoSkinFor once
// the terminal reports its background, so a light terminal reads the
// light skin. Naming only the dark one hides the file that is driving
// the styles, on the page whose whole job is naming the files read.
func TestBuildConfigReport_ListsBothAutoSkinCandidates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, theme.SkinsDir, theme.AutoSkinFor(true)+".yaml"))
	writeFile(t, filepath.Join(dir, theme.SkinsDir, theme.AutoSkinFor(false)+".yaml"))

	in := buildConfigReport(configInputs{
		cfg:       &config.Config{Theme: config.Theme{Name: theme.AutoSkinName}},
		configDir: dir,
		capture:   &a10rlog.Capture{},
	})()

	require.Equal(t, []config.Source{
		{Kind: config.SourceSkin, Path: filepath.Join(dir, theme.SkinsDir, theme.AutoSkinFor(true)+".yaml")},
		{Kind: config.SourceSkin, Path: filepath.Join(dir, theme.SkinsDir, theme.AutoSkinFor(false)+".yaml")},
	}, in.Sources)
}

// The warnings are read at render time, not at build time, so `r` on
// the page shows what the most recent load warned about.
func TestBuildConfigReport_ReadsTheWarningsAtRenderTime(t *testing.T) {
	t.Parallel()

	capture := &a10rlog.Capture{}
	report := buildConfigReport(configInputs{
		cfg:       &config.Config{},
		configDir: t.TempDir(),
		capture:   capture,
	})
	require.Empty(t, report().Warnings)

	logger := captureTestLogger(capture)
	capture.Start()
	logger.Warn("unknown skin")
	capture.Stop()

	require.Equal(t, []string{"unknown skin"}, report().Warnings)
}

func captureTestLogger(c *a10rlog.Capture) *slog.Logger {
	var sink bytes.Buffer
	return slog.New(c.Wrap(slog.NewTextHandler(&sink, nil)))
}

// `:reload` re-reads the tree, and the two reports shipped alongside
// it are the first place a user looks afterwards. A source list
// frozen at boot answers "which of my files did a10r read" with the
// set from before the edit.
func TestBuildConfigReport_FollowsAReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := &config.Config{Theme: config.Theme{Name: "nord"}}
	render := buildConfigReport(configInputs{cfg: cfg, configDir: dir, capture: &a10rlog.Capture{}})
	require.Empty(t, render().Sources)

	writeFile(t, filepath.Join(dir, config.AliasesFile))
	writeFile(t, filepath.Join(dir, theme.SkinsDir, "gruvbox.yaml"))
	cfg.Theme.Name = "gruvbox"

	require.Equal(t, []config.Source{
		{Kind: config.SourceAliases, Path: filepath.Join(dir, config.AliasesFile)},
		{Kind: config.SourceSkin, Path: filepath.Join(dir, theme.SkinsDir, "gruvbox.yaml")},
	}, render().Sources)
}

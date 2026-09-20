// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"os"
	"path/filepath"

	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/report"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// configInputs are the startup facts the `:config` report needs.
type configInputs struct {
	cfg       *config.Config
	configDir string
	capture   *a10rlog.Capture
}

// buildConfigReport returns the renderer the `:config` page calls.
// The source list starts from what config.Load recorded (the base
// file and its drop-ins) and adds the three overlays the loader never
// sees, because the page answers "which of my files did a10r read"
// and the operator does not care which package read them.
func buildConfigReport(in configInputs) func() report.ConfigInput {
	sources := append([]config.Source(nil), in.cfg.Sources...)
	sources = appendIfPresent(sources, config.SourceAliases,
		filepath.Join(in.configDir, config.AliasesFile))
	sources = appendIfPresent(sources, config.SourceKeys,
		filepath.Join(in.configDir, config.KeysDir, config.DefaultKeysProfile+".yaml"))
	for _, name := range skinFileNames(in.cfg.Theme.Name) {
		sources = appendIfPresent(sources, config.SourceSkin,
			filepath.Join(in.configDir, theme.SkinsDir, name))
	}

	return func() report.ConfigInput {
		return report.ConfigInput{
			Sources: sources,
			// Read per render rather than captured at build time, so
			// `r` shows the most recent window rather than a snapshot
			// frozen when the page was first wired.
			Warnings: in.capture.Messages(),
		}
	}
}

// appendIfPresent lists a file only when it exists. An operator who
// curates none of the overlays must not read three lines naming
// files they never created.
func appendIfPresent(sources []config.Source, kind config.SourceKind, path string) []config.Source {
	if _, err := os.Stat(path); err != nil {
		return sources
	}
	return append(sources, config.Source{Kind: kind, Path: path})
}

// skinFileNames lists the user skin files a start can read for a
// theme name, because the sentinel is not a filename and a user skin
// shadows the resolved name, not the sentinel. Under `auto` the App
// re-loads through theme.AutoSkinFor once the terminal reports its
// background, so both candidates are in play and the report names
// whichever ones exist rather than guessing the background.
func skinFileNames(themeName string) []string {
	if isAutoTheme(themeName) {
		return []string{theme.AutoSkinFor(true) + ".yaml", theme.AutoSkinFor(false) + ".yaml"}
	}
	return []string{themeName + ".yaml"}
}

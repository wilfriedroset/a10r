// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"log/slog"
	"path/filepath"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// isAutoTheme reports whether the resolved theme name leaves the
// skin choice to the terminal background. Empty counts as auto
// because the resolved default is the sentinel.
func isAutoTheme(name string) bool {
	return name == "" || name == theme.AutoSkinName
}

// defaultLoadStyles is the production wiring for Deps.LoadStyles.
// Compiles the requested theme; the auto sentinel (and an empty
// name) falls back to the default dark skin, which the App then
// swaps for the light one if the terminal reports a light
// background. configDir is the resolved config-dir root (per ADR
// 0027) — user-supplied skins live in <configDir>/skins/<name>.yaml
// and shadow bundled skins of the same name with a logged warning.
func defaultLoadStyles(name, configDir string) (*theme.Styles, error) {
	if isAutoTheme(name) {
		name = theme.DefaultSkinName
	}
	loader := &theme.Loader{
		UserDir: filepath.Join(configDir, "skins"),
		Logger:  slog.Default(),
	}
	return loader.Load(name) //nolint:wrapcheck // Loader.Load already wraps with the skin path.
}

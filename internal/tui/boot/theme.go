// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"log/slog"
	"path/filepath"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// defaultLoadStyles is the production wiring for Deps.LoadStyles.
// Compiles the requested theme; empty name falls back to the
// default skin name. configDir is the resolved config-dir root
// (per ADR 0027) — user-supplied skins live in
// <configDir>/skins/<name>.yaml and shadow bundled skins of the
// same name with a logged warning.
func defaultLoadStyles(name, configDir string) (*theme.Styles, error) {
	if name == "" {
		name = theme.DefaultSkinName
	}
	loader := &theme.Loader{
		UserDir: filepath.Join(configDir, "skins"),
		Logger:  slog.Default(),
	}
	return loader.Load(name) //nolint:wrapcheck // Loader.Load already wraps with the skin path.
}

// SPDX-License-Identifier: Apache-2.0

package theme

import "embed"

// bundledSkins ships three default skins (catppuccin-mocha,
// catppuccin-latte, gruvbox-dark) inside the binary so a fresh
// install needs no on-disk theme files. Per M1, user-supplied skins
// at <config-dir>/skins/<name>.yaml take precedence — that
// resolution lives in loader.go.
//
//go:embed skins/*.yaml
var bundledSkins embed.FS

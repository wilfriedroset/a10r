// SPDX-License-Identifier: Apache-2.0

package theme

import "embed"

// bundledSkins ships the eight catppuccin variants (frappe / latte
// / macchiato / mocha, each with a `-transparent` sibling that uses
// `bgColor: default` to inherit the terminal's native background).
// SOURCES.yaml records the upstream pin and license; refresh via
// `make skins-sync`.
//
//go:embed skins/*.yaml
var bundledSkins embed.FS

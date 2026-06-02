// SPDX-License-Identifier: Apache-2.0

package theme

import "embed"

// bundledSkins ships the in-binary skin set: the catppuccin
// variants (frappe / latte / macchiato / mocha, each with a
// `-transparent` sibling that uses `bgColor: default` to inherit
// the terminal's native background) plus the in-tree-authored
// entries. SOURCES.yaml records every entry's origin and license;
// refresh synced skins via `make skins-sync`.
//
//go:embed skins/*.yaml
var bundledSkins embed.FS

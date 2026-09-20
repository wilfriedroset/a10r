// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Names lists every skin Load can resolve: the bundled set plus the
// basenames of the `.yaml` files in userDir, deduped and sorted. The
// `:skin` picker offers this list, so it has to follow the loader's
// own rules -- a name the loader would refuse is an entry the user
// could select and not apply.
//
// A missing or unreadable userDir yields the bundled set alone,
// because a host with no skins directory is the common case rather
// than an error.
func Names(userDir string) []string {
	names := bundledSkinNames()
	if userDir != "" {
		names = append(names, userNames(userDir)...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func bundledSkinNames() []string {
	entries, err := fs.ReadDir(bundledSkins, SkinsDir)
	if err != nil {
		// The embed.FS is built at compile time, so this cannot fail in
		// a binary that links.
		return nil
	}
	return skinNamesFrom(entries)
}

func userNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return skinNamesFrom(entries)
}

func skinNamesFrom(entries []fs.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		// The sentinel passes validSkinName but is not loadable: Load
		// reserves it, and the wiring maps it to the default skin before
		// Load sees it, so offering it would repaint to something else.
		if !validSkinName.MatchString(name) || name == AutoSkinName {
			continue
		}
		names = append(names, name)
	}
	return names
}

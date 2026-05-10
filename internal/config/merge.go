// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// configDropInDir is the directory name (relative to the resolved
// config dir) where drop-in YAML fragments live. The shape mirrors
// systemd's *.d/ override convention so ops teams can ship
// per-environment tenant snippets via configuration management
// without rewriting the user's hand-edited base.
const configDropInDir = "config.d"

// discoverDropIns walks dir recursively and returns the absolute paths
// of every *.yaml / *.yml file found, in lexical order. Symlinks are
// followed for both files and directories so an ops team can stage a
// shared snippet under /etc/a10r/snippets/ and link it into
// $XDG_CONFIG_HOME/a10r/config.d/ without copying.
//
// A missing dir is not an error — operators who do not curate drop-ins
// pay nothing for the feature. Returns (nil, nil) in that case.
//
// Symlink loops are guarded by a visited-set keyed on the resolved
// absolute path; revisiting the same directory short-circuits silently
// rather than spinning. Non-loop traversal errors (permission denied
// on a subdir, broken symlink target) surface to the caller so the
// operator notices a misconfigured drop-in tree rather than missing
// its contents on the next run.
func discoverDropIns(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat drop-in dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("drop-in path %q is not a directory", dir)
	}

	var found []string
	visited := map[string]struct{}{}
	if err := walkSymlinks(dir, visited, &found); err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

// walkSymlinks is the recursive body of discoverDropIns. It uses
// os.Stat (not Lstat) so symlinks are followed transparently, and
// keeps a visited-set keyed on the resolved canonical path so
// circular symlinks resolve to a no-op rather than infinite recursion.
func walkSymlinks(dir string, visited map[string]struct{}, found *[]string) error {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve symlink %q: %w", dir, err)
	}
	if _, seen := visited[canonical]; seen {
		return nil
	}
	visited[canonical] = struct{}{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read drop-in dir %q: %w", dir, err)
	}

	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		// os.Stat follows symlinks; entry.Type() reflects the symlink
		// itself, so a directory linked from inside config.d/ would be
		// skipped without this. The result drives the recurse-vs-collect
		// branch below.
		info, err := os.Stat(full)
		if err != nil {
			return fmt.Errorf("stat drop-in entry %q: %w", full, err)
		}
		switch {
		case info.IsDir():
			if err := walkSymlinks(full, visited, found); err != nil {
				return err
			}
		case info.Mode().IsRegular() && hasYAMLSuffix(entry.Name()):
			abs, err := filepath.Abs(full)
			if err != nil {
				return fmt.Errorf("absolute path %q: %w", full, err)
			}
			*found = append(*found, abs)
		}
	}
	return nil
}

// hasYAMLSuffix reports whether name ends in .yaml or .yml, matching
// the case we accept in user-authored fragments. Dotfiles (e.g.
// editor swap files like `.foo.yaml.swp`) are not specifically
// excluded here because the suffix check is sufficient — `.swp` is
// not `.yaml`.
func hasYAMLSuffix(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

// mergeInto folds overlay onto base in place. Last-key-wins for
// scalar fields (Defaults, Theme, Log, Pages) at the field level —
// only non-zero overlay values overwrite the base. Backends are
// appended; a duplicate name across sources is a fail-closed error
// per the open-questions decision (the user almost certainly meant
// distinct tenants and a silent collision would route writes to the
// wrong backend).
//
// The backendSource map carries every already-claimed backend name
// to its source file so the duplicate-name error can echo BOTH
// originating files — the operator needs to find the conflict in a
// possibly-large drop-in tree. Callers seed the map with the base
// file's backends before the first overlay merges.
func mergeInto(base, overlay *Config, overlayPath string, backendSource map[string]string) error {
	for i := range overlay.Backends {
		b := overlay.Backends[i]
		if existing, dup := backendSource[b.Name]; dup {
			return fmt.Errorf("duplicate backend name %q: defined in %q and %q", b.Name, existing, overlayPath)
		}
		backendSource[b.Name] = overlayPath
		base.Backends = append(base.Backends, b)
	}

	mergeDefaults(&base.Defaults, overlay.Defaults)
	mergeTheme(&base.Theme, overlay.Theme)
	mergeLog(&base.Log, overlay.Log)
	mergePages(&base.Pages, overlay.Pages)
	// Keys is reserved-empty today (J2). When fields land they merge
	// here under the same non-zero-wins rule.
	return nil
}

// mergeDefaults applies non-zero fields from overlay onto base. A
// drop-in that sets only `defaults.poll_interval: 60s` must override
// poll_interval without nuking read_only or log_format from the base.
//
// ReadOnly is the one-way escape-hatch documented under "Read-only
// mode" in configuration.md (any-true wins). A drop-in that sets it
// to true wins over a base `false`; a drop-in zero leaves the base
// alone. False-as-explicit-override is unreachable through this path
// — the user must edit the base file or use --read-only.
func mergeDefaults(base *Defaults, overlay Defaults) {
	if overlay.PollInterval != 0 {
		base.PollInterval = overlay.PollInterval
	}
	if overlay.ReadOnly {
		base.ReadOnly = true
	}
	if overlay.LogFormat != "" {
		base.LogFormat = overlay.LogFormat
	}
	if overlay.BulkConcurrency != 0 {
		base.BulkConcurrency = overlay.BulkConcurrency
	}
}

func mergeTheme(base *Theme, overlay Theme) {
	if overlay.Name != "" {
		base.Name = overlay.Name
	}
}

func mergeLog(base *Log, overlay Log) {
	if overlay.Path != "" {
		base.Path = overlay.Path
	}
	if overlay.Level != "" {
		base.Level = overlay.Level
	}
}

func mergePages(base *PageOverrides, overlay PageOverrides) {
	mergePage(&base.Alerts, overlay.Alerts)
	mergePage(&base.Silences, overlay.Silences)
	mergePage(&base.Groups, overlay.Groups)
	mergePage(&base.Receivers, overlay.Receivers)
	mergePage(&base.Status, overlay.Status)
}

func mergePage(base *PageConfig, overlay PageConfig) {
	if overlay.PollInterval != 0 {
		base.PollInterval = overlay.PollInterval
	}
}

// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultSkinName is the v0.1 default per M1.
const DefaultSkinName = "catppuccin-mocha"

// skinsDir is the basename used both for the embedded skins/
// directory and for the user-side <config-dir>/skins/ directory.
const skinsDir = "skins"

// ErrInvalidSkin wraps every parse / compile failure so callers can
// branch on a stable sentinel via errors.Is. Used by the loader for
// both bundled and user skins.
var ErrInvalidSkin = errors.New("invalid skin")

// Loader resolves and parses skin files. UserDir is the
// <config-dir>/skins/ directory (empty disables user skins). Logger
// is used for shadow warnings (user file shadows bundled) and
// fallback warnings (unknown name → DefaultSkinName); pass a no-op
// logger when neither matters.
type Loader struct {
	UserDir string
	Logger  *slog.Logger
}

// Load resolves the skin named by name and returns its compiled
// Styles. Resolution order:
//
//  1. <UserDir>/<name>.yaml — when UserDir is set and the file
//     exists. A shadow-warning is logged when the same name also
//     ships in the bundled set so the operator can spot accidental
//     overrides.
//  2. embedded skins/<name>.yaml — the bundled set.
//  3. embedded skins/catppuccin-mocha.yaml — fallback when name is
//     unknown. A warning is logged so the operator knows the
//     requested skin was missing.
//
// Empty name short-circuits to DefaultSkinName.
//
// A malformed or invalid skin always surfaces as an error wrapping
// ErrInvalidSkin — callers should NOT silently continue past one,
// since rendering with a half-built Styles is worse than crashing
// at startup.
func (l *Loader) Load(name string) (*Styles, error) {
	if name == "" {
		name = DefaultSkinName
	}

	raw, fromUser, ok := l.findSkin(name)
	if !ok {
		l.warnUnknown(name)
		return l.loadBundled(DefaultSkinName)
	}
	if fromUser && bundledExists(name) {
		l.warnShadow(name)
	}

	return parseAndCompile(raw, name)
}

// findSkin returns the raw bytes of the named skin. fromUser is
// true when the file came from UserDir.
func (l *Loader) findSkin(name string) (raw []byte, fromUser, ok bool) {
	if l.UserDir != "" {
		path := filepath.Join(l.UserDir, name+".yaml")

		if data, err := os.ReadFile(path); err == nil {
			return data, true, true
		}
	}
	if data, err := readBundled(name); err == nil {
		return data, false, true
	}
	return nil, false, false
}

// loadBundled is the fallback path used when the requested skin is
// unknown. It MUST succeed for DefaultSkinName since that file ships
// in the embed.FS — anything else surfaces as an error so the
// operator notices rather than getting a blank-styles UI.
func (l *Loader) loadBundled(name string) (*Styles, error) {
	raw, err := readBundled(name)
	if err != nil {
		return nil, fmt.Errorf("%w: bundled %q: %w", ErrInvalidSkin, name, err)
	}
	return parseAndCompile(raw, name)
}

func (l *Loader) warnUnknown(name string) {
	if l.Logger == nil {
		return
	}
	l.Logger.Warn("unknown skin; falling back to default",
		slog.String("requested", name),
		slog.String("default", DefaultSkinName),
	)
}

func (l *Loader) warnShadow(name string) {
	if l.Logger == nil {
		return
	}
	l.Logger.Warn("user skin shadows bundled skin of the same name",
		slog.String("name", name),
		slog.String("user_path", filepath.Join(l.UserDir, name+".yaml")),
	)
}

// readBundled returns the raw bytes of the named bundled skin. The
// embed path is `skins/<name>.yaml`.
func readBundled(name string) ([]byte, error) {
	path := skinsDir + "/" + name + ".yaml"
	data, err := bundledSkins.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bundled %q: %w", path, err)
	}
	return data, nil
}

// bundledExists reports whether name is in the embedded set.
func bundledExists(name string) bool {
	_, err := fs.Stat(bundledSkins, skinsDir+"/"+name+".yaml")
	return err == nil
}

// parseAndCompile is the shared parse → validate → compile pipeline
// used by both the user and bundled paths.
func parseAndCompile(raw []byte, name string) (*Styles, error) {
	var f skinFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrInvalidSkin, name, err)
	}
	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrInvalidSkin, name, err)
	}
	styles, err := compile(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrInvalidSkin, name, err)
	}
	return styles, nil
}

// BundledNames returns the list of bundled skin names (without the
// .yaml suffix). Useful for `a10r info` and the wizard, and for
// validating that the embed.FS is populated as expected.
func BundledNames() ([]string, error) {
	entries, err := fs.ReadDir(bundledSkins, skinsDir)
	if err != nil {
		return nil, fmt.Errorf("read bundled skins dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		names = append(names, name[:len(name)-len(".yaml")])
	}
	return names, nil
}

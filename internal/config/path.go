// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path/filepath"

	"github.com/wilfriedroset/a10r/internal/xdg"
)

// a10r-specific resolution constants for config-dir override and the
// file basename inside the resolved directory.
const (
	envConfigDir      = "A10R_CONFIG_DIR"
	defaultConfigFile = "a10r.yaml"
)

const (
	goosDarwin  = "darwin"
	goosWindows = "windows"
)

// DefaultDir returns the OS-conformant config directory (the
// "built-in default" rung of ADR 0027's precedence chain):
//
//   - Unix:    $XDG_CONFIG_HOME/a10r (default ~/.config/a10r)
//   - macOS:   ~/Library/Application Support/a10r
//   - Windows: %LOCALAPPDATA%\a10r
func DefaultDir() (string, error) {
	return defaultConfigDirFor(hostGOOS(), hostGetenv, hostHomeDir)
}

// defaultConfigDirFor is the testable core of DefaultDir; env and
// homeDir let one host exercise every GOOS branch without build tags.
func defaultConfigDirFor(
	goos string,
	env func(string) string,
	homeDir func() (string, error),
) (string, error) {
	switch goos {
	case goosDarwin:
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("user home: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "a10r"), nil

	case goosWindows:
		local := env(xdg.LocalAppData)
		if local == "" {
			return "", xdg.ErrLocalAppDataMissing
		}
		return filepath.Join(local, "a10r"), nil

	default: // linux + other unix
		if cfg := env(xdg.ConfigHome); cfg != "" {
			return filepath.Join(cfg, "a10r"), nil
		}
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("user home: %w", err)
		}
		return filepath.Join(home, ".config", "a10r"), nil
	}
}

// resolveConfigDir applies ADR 0027 precedence for the config
// directory: explicit (CLI flag) > A10R_CONFIG_DIR env > OS XDG
// default.
func resolveConfigDir(
	explicit string,
	env func(string) string,
	homeDir func() (string, error),
	goos string,
) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if envDir := env(envConfigDir); envDir != "" {
		return envDir, nil
	}
	return defaultConfigDirFor(goos, env, homeDir)
}

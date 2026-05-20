// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// xdgConfigHome and localAppData are the env vars consulted on Unix
// and Windows respectively for the XDG-style config directory
// resolution (the env-var slot in ADR 0027's precedence chain).
// localAppData mirrors the constant in internal/log/path.go; once a
// third package needs XDG resolution we'll factor both into
// internal/xdg.
const (
	xdgConfigHome = "XDG_CONFIG_HOME"
	localAppData  = "LOCALAPPDATA"
)

// a10r-specific resolution constants for config-dir override and the
// file basename inside the resolved directory.
const (
	envConfigDir      = "A10R_CONFIG_DIR"
	defaultConfigFile = "a10r.yaml"
)

// errLocalAppDataMissing is returned by defaultConfigDirFor on
// Windows when %LOCALAPPDATA% is unset — there is no sensible
// fallback on Windows without it.
var errLocalAppDataMissing = errors.New("LOCALAPPDATA not set")

// DefaultDir returns the OS-conformant config directory (the
// "built-in default" rung of ADR 0027's precedence chain):
//
//   - Unix:    $XDG_CONFIG_HOME/a10r (default ~/.config/a10r)
//   - macOS:   ~/Library/Application Support/a10r
//   - Windows: %LOCALAPPDATA%\a10r
func DefaultDir() (string, error) {
	return defaultConfigDirFor(hostGOOS(), hostGetenv, hostHomeDir)
}

// defaultConfigDirFor is the testable core of DefaultDir. The env
// and homeDir func parameters are the injection points so the test
// can drive every OS branch from a single host without build tags.
func defaultConfigDirFor(
	goos string,
	env func(string) string,
	homeDir func() (string, error),
) (string, error) {
	switch goos {
	case "darwin":
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("user home: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "a10r"), nil

	case "windows":
		local := env(localAppData)
		if local == "" {
			return "", errLocalAppDataMissing
		}
		return filepath.Join(local, "a10r"), nil

	default: // linux + other unix
		if cfg := env(xdgConfigHome); cfg != "" {
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

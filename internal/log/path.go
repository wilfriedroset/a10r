// SPDX-License-Identifier: Apache-2.0

package log

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// xdgStateHome and localAppData are the env vars consulted on Unix
// and Windows respectively. Pulled out as constants so the test
// helper can reference them without string drift.
const (
	xdgStateHome = "XDG_STATE_HOME"
	localAppData = "LOCALAPPDATA"
)

// errLocalAppDataMissing is returned by defaultPathFor on Windows
// when %LOCALAPPDATA% is unset — there is no sensible fallback on
// Windows without it.
var errLocalAppDataMissing = errors.New("LOCALAPPDATA not set")

// DefaultPath returns the OS-conformant log file path per
// open-question D1:
//
//   - Unix:    $XDG_STATE_HOME/a10r/a10r.log (default
//     ~/.local/state/a10r/a10r.log when XDG_STATE_HOME is unset)
//   - macOS:   ~/Library/Logs/a10r/a10r.log
//   - Windows: %LOCALAPPDATA%\a10r\Logs\a10r.log
func DefaultPath() (string, error) {
	return defaultPathFor(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

// defaultPathFor is the testable core of DefaultPath. The env and
// homeDir func parameters are injection points so the caller can
// drive every OS branch from a single host without build tags.
func defaultPathFor(
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
		return filepath.Join(home, "Library", "Logs", "a10r", "a10r.log"), nil

	case "windows":
		local := env(localAppData)
		if local == "" {
			return "", errLocalAppDataMissing
		}
		return filepath.Join(local, "a10r", "Logs", "a10r.log"), nil

	default: // linux + other unix
		if state := env(xdgStateHome); state != "" {
			return filepath.Join(state, "a10r", "a10r.log"), nil
		}
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("user home: %w", err)
		}
		return filepath.Join(home, ".local", "state", "a10r", "a10r.log"), nil
	}
}

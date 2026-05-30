// SPDX-License-Identifier: Apache-2.0

package log

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wilfriedroset/a10r/internal/xdg"
)

const (
	goosDarwin  = "darwin"
	goosWindows = "windows"
)

// DefaultPath returns the OS-conformant log file path:
//
//   - Unix:    $XDG_STATE_HOME/a10r/a10r.log (default
//     ~/.local/state/a10r/a10r.log when XDG_STATE_HOME is unset)
//   - macOS:   ~/Library/Logs/a10r/a10r.log
//   - Windows: %LOCALAPPDATA%\a10r\Logs\a10r.log
func DefaultPath() (string, error) {
	return defaultPathFor(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

// defaultPathFor is the testable core of DefaultPath; env and
// homeDir let one host exercise every GOOS branch without build tags.
func defaultPathFor(
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
		return filepath.Join(home, "Library", "Logs", "a10r", "a10r.log"), nil

	case goosWindows:
		local := env(xdg.LocalAppData)
		if local == "" {
			return "", xdg.ErrLocalAppDataMissing
		}
		return filepath.Join(local, "a10r", "Logs", "a10r.log"), nil

	default: // linux + other unix
		if state := env(xdg.StateHome); state != "" {
			return filepath.Join(state, "a10r", "a10r.log"), nil
		}
		home, err := homeDir()
		if err != nil {
			return "", fmt.Errorf("user home: %w", err)
		}
		return filepath.Join(home, ".local", "state", "a10r", "a10r.log"), nil
	}
}

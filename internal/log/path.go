// SPDX-License-Identifier: Apache-2.0

package log

import (
	"fmt"
	"log/slog"
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

// ReportPath names the file the sink opens. An unresolvable default
// reports a sentinel rather than an error, like the state dir: a
// missing HOME is what the operator runs info to find out.
func ReportPath(configured string) string {
	return reportPathFor(configured, DefaultPath)
}

func reportPathFor(configured string, def func() (string, error)) string {
	if configured != "" {
		return configured
	}
	path, err := def()
	if err != nil {
		slog.Debug("no default log path for the info report", slog.Any("err", err))
		return "(unresolved)"
	}
	return path
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

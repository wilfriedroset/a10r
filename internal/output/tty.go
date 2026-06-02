// SPDX-License-Identifier: Apache-2.0

package output

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// IsTerminal reports whether f is connected to a real terminal
// (a TTY/PTY). Used by ResolveForFile and any caller that needs
// to pick between table-on-tty and json-on-pipe without a separate
// flag.
//
// The probe uses charmbracelet/x/term.IsTerminal (already an
// indirect dep via bubbletea v2) which performs the proper
// platform-specific termios call. A naïve check on
// os.ModeCharDevice incorrectly classifies /dev/null and other
// character-special devices as terminals — that bug would surface
// the moment a CI step pipes through `</dev/null` or `>/dev/tty1`,
// rendering a coloured table where downstream tools expect JSON.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}

// ResolveForFile combines IsTerminal and Resolve so callers do not
// have to plumb both. Pairs the file (typically os.Stdout) with a
// user-supplied format string (which may be empty) and returns the
// effective Format. Centralising the wiring prevents drift across
// the read-only commands as they land.
func ResolveForFile(format Format, f *os.File) Format {
	return Resolve(format, IsTerminal(f))
}

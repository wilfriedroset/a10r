// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"log/slog"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/boot"
)

// levelFor wraps boot.LevelFor for cmd-internal callers.
func levelFor(debug, quiet bool) slog.Level {
	return boot.LevelFor(debug, quiet)
}

// userAgent wraps boot.UserAgent so every non-TUI subcommand tags
// its outgoing HTTP with the same User-Agent shape.
func userAgent(ver, comm string) string {
	return boot.UserAgent(ver, comm)
}

// loadOptsFromFlags wraps boot.LoadOptsFromFlags so every read-only
// subcommand agrees on how the persistent flags map to LoadOpts.
func loadOptsFromFlags(flags *GlobalFlags) config.LoadOpts {
	return boot.LoadOptsFromFlags(flags)
}

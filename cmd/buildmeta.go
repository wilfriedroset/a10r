// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"log/slog"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/boot"
)

// levelFor is the cmd-side wrapper around boot.LevelFor. The
// pre-extraction version of this helper lived in cmd/tui.go;
// after the boot extraction the canonical implementation moved
// into the boot package, but the symbol is still referenced from
// every non-TUI subcommand's --debug-http logger
// (cmd/logger.go) and from this file's siblings, so the
// wrapper keeps cmd-internal call sites stable.
func levelFor(debug, quiet bool) slog.Level {
	return boot.LevelFor(debug, quiet)
}

// userAgent is the cmd-side wrapper around boot.UserAgent. Same
// rationale as levelFor: every non-TUI alerts / silences / etc.
// subcommand tags its outgoing HTTP with a User-Agent built from
// the ldflag-injected version + commit pair, and routing the
// call through this wrapper keeps the import surface bounded.
func userAgent(ver, comm string) string {
	return boot.UserAgent(ver, comm)
}

// loadOptsFromFlags is the cmd-side wrapper around
// boot.LoadOptsFromFlags. Used by every read-only subcommand
// (alerts, silences, groups, receivers, doctor, info, validate)
// so they all agree on how the persistent flags map to LoadOpts.
func loadOptsFromFlags(flags *GlobalFlags) config.LoadOpts {
	return boot.LoadOptsFromFlags(flags)
}

// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"fmt"
	"io"
	"log/slog"
)

// LevelFor folds the resolved --debug / --quiet bits into a slog
// level. Default is Info; --debug bumps to Debug; --quiet drops to
// Warn. The two cannot both be true here — reconcileLogLevelFlags
// in cmd/root.go has already converted "both set" into "debug wins".
//
// Exported so the cmd package routes its non-TUI subcommands'
// --debug-http logger through the same fold without duplicating
// the switch.
func LevelFor(debug, quiet bool) slog.Level {
	switch {
	case debug:
		return slog.LevelDebug
	case quiet:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// closeLogger flushes and releases the logger sink. Surfaces any
// failure to errOut without escalating because Close() runs in a
// defer where the program is already exiting; a non-fatal warning
// is the most useful thing the operator can see.
func closeLogger(closer io.Closer, errOut io.Writer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		fmt.Fprintf(errOut, "warning: log file close failed: %v\n", err)
	}
}

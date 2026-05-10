// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"log/slog"

	a10rlog "github.com/wilfriedroset/a10r/internal/log"
)

// buildHTTPDebugLogger initialises the project logger for non-TUI
// subcommands that need to emit transport.WithDebugLog records
// when --debug-http is set. Returns (nil, no-op-closer, nil) when
// the flag is not set so callers can always defer the close
// without conditional plumbing.
//
// The returned logger writes to the path resolved by the loader
// (or the XDG-default when the config file does not exist —
// e.g. `a10r doctor` on a fresh machine before `a10r init`). The
// closer flushes the lumberjack rotation buffer on shutdown;
// without the explicit close the last record can be lost when a
// short-lived command exits before the file write lands.
//
// errOut is reserved for future fallback-warning routing — the
// underlying logger already emits its own warning on the
// returned Logger when the configured path is unwritable, so the
// parameter is currently a no-op kept on the signature so a
// future refactor can pipe lumberjack rotation events here
// without churning every call site.
func buildHTTPDebugLogger(flags *GlobalFlags, _ io.Writer) (*slog.Logger, io.Closer, error) {
	if !flags.DebugHTTP {
		return nil, noopCloser{}, nil
	}
	logger, closer, err := a10rlog.New(a10rlog.Opts{
		Path:   flags.LogPath,
		Format: a10rlog.Format(flags.LogFormat),
		Level:  levelFor(flags.Debug, flags.Quiet),
	})
	if err != nil {
		return nil, noopCloser{}, fmt.Errorf("init http debug logger: %w", err)
	}
	return logger, closer, nil
}

// noopCloser is the placeholder returned when --debug-http is not
// set so callers can `defer closer.Close()` unconditionally
// without wrapping the closer in another nil check.
type noopCloser struct{}

// Close implements io.Closer. Always nil.
func (noopCloser) Close() error { return nil }

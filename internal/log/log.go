// SPDX-License-Identifier: Apache-2.0

// Package log builds the project's *slog.Logger from runtime
// configuration. JSON and logfmt are the two configurable output
// formats; pretty/colour TTY output is deliberately not provided so
// the log file stays grep- and jq-friendly without ANSI escapes.
package log

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Format identifies the log output format.
type Format string

const (
	// FormatJSON renders each record as a JSON object via slog.JSONHandler.
	FormatJSON Format = "json"

	// FormatLogfmt renders each record as space-separated key=value
	// pairs via slog.TextHandler. The format is what observability
	// backends (Loki, Datadog, Splunk) treat as logfmt.
	FormatLogfmt Format = "logfmt"

	// DefaultFormat is used when Opts.Format is the empty string.
	DefaultFormat = FormatLogfmt

	// maxLogSizeMB and maxLogBackups bound on-disk log retention:
	// rotate at 10 MB, keep 3 historical files.
	maxLogSizeMB  = 10
	maxLogBackups = 3
	logDirPerm    = 0o755
)

// ErrUnknownFormat is returned by New when Opts.Format is set to a
// value other than "json" or "logfmt".
var ErrUnknownFormat = errors.New("unknown log format")

// Opts configures the logger factory. The zero value is valid:
// Format defaults to logfmt, Level defaults to slog.LevelInfo, Path
// defaults to the OS-conformant DefaultPath, Stderr defaults to false
// (write to file).
type Opts struct {
	Format Format
	Level  slog.Level
	Path   string
	Stderr bool
}

// sinkOpener is the unit-test seam for sink resolution. It returns
// the writer to attach to the slog.Handler, the closer the program
// must drive on shutdown, the path that was attempted (empty when
// resolution itself failed or the sink is stderr by request), and a
// fallback error when degradation to stderr happened so the caller
// can log a warning describing the cause.
type sinkOpener func(Opts) (io.Writer, io.Closer, string, error)

// New builds a *slog.Logger from opts. The returned io.Closer must
// be Closed when the program exits to flush any rotation buffers
// and release the underlying file handle. The Closer is always
// non-nil; for Stderr=true it is a no-op so callers can `defer
// closer.Close()` unconditionally.
//
// If opts.Path is set (or DefaultPath returns one) but the file
// cannot be opened, New falls back to stderr and logs a single
// warning on the returned logger so the fallback is observable; it
// does not return an error in that case.
//
// New does not accept a context.Context: the logger has no I/O loop
// to cancel. A future Reload(ctx) on rotation/SIGHUP will, but the
// containedctx linter (see .golangci.yml) keeps that ctx out of any
// struct field.
func New(opts Opts) (*slog.Logger, io.Closer, error) {
	return newWithOpener(opts, openSink)
}

// newWithOpener is the test-injectable core of New. Production code
// always reaches it via New (which passes the real openSink).
func newWithOpener(opts Opts, openSinkFn sinkOpener) (*slog.Logger, io.Closer, error) {
	format, err := normaliseFormat(opts.Format)
	if err != nil {
		return nil, nil, err
	}

	writer, closer, attemptedPath, fallbackErr := openSinkFn(opts)
	logger := slog.New(newHandler(format, opts.Level, writer))
	if fallbackErr != nil {
		emitFallbackWarning(logger, attemptedPath, fallbackErr)
	}
	return logger, closer, nil
}

// emitFallbackWarning writes the single fallback notice on the
// supplied logger. Path is omitted when empty so callers do not see
// a misleading `path=""` for the case where DefaultPath itself
// failed (the resolution stage error lives in the reason field).
func emitFallbackWarning(logger *slog.Logger, attemptedPath string, cause error) {
	attrs := make([]any, 0, 2)
	attrs = append(attrs, slog.String("reason", cause.Error()))
	if attemptedPath != "" {
		attrs = append(attrs, slog.String("path", attemptedPath))
	}
	logger.Warn("log file unwritable; falling back to stderr", attrs...)
}

// normaliseFormat validates and defaults Opts.Format.
func normaliseFormat(f Format) (Format, error) {
	switch f {
	case "":
		return DefaultFormat, nil
	case FormatJSON, FormatLogfmt:
		return f, nil
	default:
		return "", fmt.Errorf("%w: %q (want %q or %q)",
			ErrUnknownFormat, f, FormatJSON, FormatLogfmt)
	}
}

// openSink returns the sink implied by opts. When Stderr is true (or
// when a file open fails), the writer is os.Stderr and the closer is
// a no-op; otherwise it is a *lumberjack.Logger that rotates by size.
//
// The third return value carries the path that was attempted (empty
// when Stderr was requested directly or when DefaultPath failed
// before yielding one) so the caller can include it in any fallback
// warning. The fourth return value is set only on fallback to stderr;
// callers treat it as a warning, not a hard error.
//
// lumberjack v2.2.1 — pinned in go.mod — implements an idempotent
// Close (subsequent calls observe a nil file handle and return nil).
// Revisit if the dep ever crosses a major boundary.
func openSink(opts Opts) (io.Writer, io.Closer, string, error) {
	if opts.Stderr {
		return os.Stderr, noopCloser{}, "", nil
	}

	path := opts.Path
	if path == "" {
		resolved, err := DefaultPath()
		if err != nil {
			return os.Stderr, noopCloser{}, "", fmt.Errorf("resolve default log path: %w", err)
		}
		path = resolved
	}

	if err := os.MkdirAll(filepath.Dir(path), logDirPerm); err != nil {
		return os.Stderr, noopCloser{}, path, fmt.Errorf("create log directory: %w", err)
	}

	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxLogSizeMB,
		MaxBackups: maxLogBackups,
		Compress:   true,
	}
	return lj, lj, path, nil
}

// newHandler returns the slog.Handler matching format, configured at
// the requested level. ReplaceAttr is wired to redactAttr so every
// log call masks the fixed secret-key set centrally — see ADR 0008
// and internal/log/redact.go. The base handler is then wrapped in
// msgRedactingHandler so URL userinfo in record.Message (which
// ReplaceAttr never sees) is also stripped.
func newHandler(format Format, level slog.Level, w io.Writer) slog.Handler {
	handlerOpts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactAttr,
	}
	var base slog.Handler
	if format == FormatJSON {
		base = slog.NewJSONHandler(w, handlerOpts)
	} else {
		base = slog.NewTextHandler(w, handlerOpts)
	}
	return &msgRedactingHandler{inner: base}
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

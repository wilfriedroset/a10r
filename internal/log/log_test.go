// SPDX-License-Identifier: Apache-2.0

package log

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_LogfmtToFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a10r.log")

	logger, closer, err := New(Opts{Format: FormatLogfmt, Level: slog.LevelInfo, Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("hello", slog.String("k", "v"), slog.Int("n", 42))
	require.NoError(t, closer.Close())

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	got := string(body)
	require.Contains(t, got, "level=INFO")
	require.Contains(t, got, "msg=hello")
	require.Contains(t, got, "k=v")
	require.Contains(t, got, "n=42")
	// logfmt sentinel: lines must not start with `{` (JSON output);
	// asserting the prefix avoids false-positives if a logfmt value
	// later includes a literal `{` inside a quoted string.
	require.False(t, strings.HasPrefix(got, "{"),
		"logfmt output must not start with `{`, got %q", got)
}

func TestNew_JSONToFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a10r.log")

	logger, closer, err := New(Opts{Format: FormatJSON, Level: slog.LevelInfo, Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("hello", slog.String("k", "v"))
	require.NoError(t, closer.Close())

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	got := string(body)
	require.True(t, strings.HasPrefix(got, "{"), "JSON output must begin with `{`, got %q", got)
	require.Contains(t, got, `"level":"INFO"`)
	require.Contains(t, got, `"msg":"hello"`)
	require.Contains(t, got, `"k":"v"`)
}

func TestNew_DefaultsToLogfmt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a10r.log")

	logger, closer, err := New(Opts{Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("hi")
	require.NoError(t, closer.Close())

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "msg=hi")
}

func TestNew_LevelFilters(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "a10r.log")

	logger, closer, err := New(Opts{Level: slog.LevelWarn, Path: path})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	logger.Info("dropped")
	logger.Warn("kept")
	require.NoError(t, closer.Close())

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	got := string(body)
	require.NotContains(t, got, "dropped")
	require.Contains(t, got, "kept")
}

func TestNew_UnknownFormat(t *testing.T) {
	t.Parallel()

	_, _, err := New(Opts{Format: Format("yaml")})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnknownFormat)
}

func TestNew_StderrSinkReturnsNoopCloser(t *testing.T) {
	t.Parallel()

	logger, closer, err := New(Opts{Stderr: true})
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, closer)
	require.NoError(t, closer.Close())
	// The closer must remain idempotent so callers can `defer Close()`.
	require.NoError(t, closer.Close())
}

func TestNew_FallsBackToStderrOnUnwritablePath(t *testing.T) {
	t.Parallel()

	// /dev/null is a file, so MkdirAll over its parent (/dev) is fine,
	// but lumberjack creating a new file inside /dev would normally
	// require root. Using a non-existent parent under a read-only
	// fixture would hit a hard MkdirAll error instead. Construct an
	// unwritable path by pointing into a regular file's name space.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, nil, 0o644))
	// "blocker" is a regular file; treating it as a directory means
	// MkdirAll on `blocker/sub` fails with ENOTDIR.
	path := filepath.Join(blocker, "sub", "a10r.log")

	logger, closer, err := New(Opts{Path: path})
	require.NoError(t, err, "fallback must not surface as a hard error")
	require.NotNil(t, logger)
	require.NotNil(t, closer)
	t.Cleanup(func() { _ = closer.Close() })

	// The unwritable path must not have been created — fallback to
	// stderr means lumberjack never opened a file there. Stat may
	// return ENOENT or ENOTDIR depending on which segment fails;
	// either way, any error confirms the path is empty.
	_, statErr := os.Stat(path)
	require.Error(t, statErr, "unwritable path must not have been created")
}

func TestNoopCloser_AlwaysNil(t *testing.T) {
	t.Parallel()

	var c noopCloser
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
}

// fakeOpenerCapturing returns an opener that yields a bytes.Buffer as
// the writer (so test asserts can read what was written), the noop
// closer, the supplied attemptedPath, and the supplied fallback err.
// Used to prove that the warning emission path in newWithOpener is
// observable even though it normally targets os.Stderr.
func fakeOpenerCapturing(buf *bytes.Buffer, attemptedPath string, fallbackErr error) sinkOpener {
	return func(Opts) (io.Writer, io.Closer, string, error) {
		return buf, noopCloser{}, attemptedPath, fallbackErr
	}
}

func TestNewWithOpener_FallbackEmitsWarningWithPath(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	opener := fakeOpenerCapturing(&buf, "/tmp/blocked.log", errors.New("simulated mkdir failure"))

	logger, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })
	require.NotNil(t, logger)

	out := buf.String()
	require.Contains(t, out, "level=WARN")
	require.Contains(t, out, "msg=\"log file unwritable; falling back to stderr\"")
	require.Contains(t, out, "path=/tmp/blocked.log")
	require.Contains(t, out, "reason=\"simulated mkdir failure\"")
}

func TestNewWithOpener_FallbackOmitsEmptyPath(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	opener := fakeOpenerCapturing(&buf, "", errors.New("DefaultPath failed"))

	_, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	out := buf.String()
	require.Contains(t, out, "level=WARN")
	require.Contains(t, out, "reason=\"DefaultPath failed\"")
	require.NotContains(t, out, "path=",
		"path attribute must be omitted when resolution itself failed")
}

func TestNewWithOpener_NoWarningOnSuccess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	opener := func(Opts) (io.Writer, io.Closer, string, error) {
		return &buf, noopCloser{}, "/tmp/ok.log", nil
	}

	_, closer, err := newWithOpener(Opts{Format: FormatLogfmt}, opener)
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	require.NotContains(t, buf.String(), "log file unwritable",
		"no warning when openSinkFn succeeds")
}

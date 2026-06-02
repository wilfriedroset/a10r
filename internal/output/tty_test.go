// SPDX-License-Identifier: Apache-2.0

package output

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsTerminal_NilFile(t *testing.T) {
	t.Parallel()

	require.False(t, IsTerminal(nil), "nil file is not a terminal")
}

func TestIsTerminal_RegularFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "regular")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	require.False(t, IsTerminal(f), "regular file is not a terminal")
}

func TestIsTerminal_DevNull(t *testing.T) {
	t.Parallel()

	// /dev/null is a character device but NOT a terminal — the
	// charmbracelet/x/term probe uses termios under the hood and
	// gets this right. The naïve os.ModeCharDevice check would
	// have classified /dev/null as a terminal, breaking
	// `a10r alerts list </dev/null` (CI / daemon use cases).
	f, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	require.False(t, IsTerminal(f), "/dev/null is a char device but not a terminal")
}

func TestResolveForFile_PipeDefaultsToJSON(t *testing.T) {
	t.Parallel()

	// A regular file is not a terminal, so empty format resolves
	// to FormatJSON via the pipe-default branch.
	dir := t.TempDir()
	path := filepath.Join(dir, "pipe")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	require.Equal(t, FormatJSON, ResolveForFile("", f))
	require.Equal(t, FormatTable, ResolveForFile(FormatTable, f),
		"explicit format wins over the pipe-default")
}

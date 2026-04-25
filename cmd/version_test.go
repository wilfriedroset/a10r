// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrintVersion_ExactFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, printVersion(&buf))

	// Pin to the exact line a `version` consumer pipes through `awk`,
	// so a regression to e.g. `a10r<version>commit=…` (missing space)
	// is caught — Contains() would let that pass.
	want := "a10r " + version + " commit=" + commit + " built=" + date + "\n"
	require.Equal(t, want, buf.String())
}

func TestVersionCommand_RegisteredOnRoot(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.SetArgs([]string{"version"})
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(io.Discard)

	require.NoError(t, rootCmd.Execute())
	require.Contains(t, outBuf.String(), "a10r")
	require.Contains(t, outBuf.String(), version)
}

func TestVersionCommand_RejectsExtraArgs(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags)
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.SetArgs([]string{"version", "extra"})
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)

	require.Error(t, rootCmd.Execute())
}

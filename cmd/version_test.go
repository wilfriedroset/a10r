// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
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

// The --version flag and the `version` subcommand must produce
// byte-identical output so scripts piping either form get the same
// line. Pinned because Cobra's default version template is
// "{{.Name}} version {{.Version}}" — a regression to the default
// would silently break consumers.
func TestRootVersionFlag_MatchesVersionSubcommand(t *testing.T) {
	t.Parallel()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags, func(*cobra.Command, *GlobalFlags) error { return nil })
	rootCmd.SetArgs([]string{"--version"})

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	require.NoError(t, rootCmd.Execute())

	want := "a10r " + version + " commit=" + commit + " built=" + date + "\n"
	require.Equal(t, want, buf.String())
}

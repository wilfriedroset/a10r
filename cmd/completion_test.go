// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// Cobra automatically registers a `completion` subcommand on any
// root that has at least one named subcommand. a10r registers
// version/info/validate in cmd.Execute, so completion appears for
// free — there is no completion.go in this package because the
// implementation is upstream cobra. These tests pin the contract
// that each of the four supported shells produces a non-empty
// script, that the subcommand appears in `--help`, and that the
// auto-generated help listing names every shell so a future cobra
// change is loud.

// completionTestRoot builds a root command with the same subcommand
// set Execute uses so the completion script the test exercises is
// the one users would actually receive. Returns the captured stdout
// buffer and a runner closure; cobra's stderr is silenced through
// io.Discard since none of the current tests assert on it.
func completionTestRoot() (out *bytes.Buffer, run func(args []string) error) {
	var flags GlobalFlags
	rootCmd := newRootCmd(&flags, nil)
	rootCmd.AddCommand(
		newVersionCmd(),
		newInfoCmd(&flags),
		newValidateCmd(&flags),
	)

	out = &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(io.Discard)

	run = func(args []string) error {
		rootCmd.SetArgs(args)
		return rootCmd.Execute()
	}
	return out, run
}

func TestCompletion_AllSupportedShells(t *testing.T) {
	t.Parallel()

	cases := []struct {
		shell    string
		mustHave string // sentinel substring proving the right script ran
	}{
		{shell: "bash", mustHave: "bash completion"},
		{shell: "zsh", mustHave: "compdef"},
		{shell: "fish", mustHave: "complete -c"},
		{shell: "powershell", mustHave: "Register-ArgumentCompleter"},
	}

	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			t.Parallel()

			outBuf, run := completionTestRoot()
			require.NoError(t, run([]string{"completion", tc.shell}),
				"completion %s must succeed", tc.shell)

			out := outBuf.String()
			require.NotEmpty(t, out,
				"completion %s must produce a non-empty script", tc.shell)
			require.Contains(t, out, tc.mustHave,
				"completion %s output must contain sentinel %q; got first 200 chars: %.200s",
				tc.shell, tc.mustHave, out)
		})
	}
}

func TestCompletion_NoArgsListsShells(t *testing.T) {
	t.Parallel()

	// `a10r completion` (no shell arg) prints cobra's auto-help that
	// lists the four supported shells. Pinning so a future cobra
	// change that silently changes that surface is loud.
	outBuf, run := completionTestRoot()
	require.NoError(t, run([]string{"completion"}))
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		require.Contains(t, outBuf.String(), shell,
			"completion help must mention the %s shell", shell)
	}
}

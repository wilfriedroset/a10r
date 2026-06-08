// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestExactlyOneArg(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "get <id>"}
	root := &cobra.Command{Use: "a10r"}
	sil := &cobra.Command{Use: "silences"}
	root.AddCommand(sil)
	sil.AddCommand(cmd)

	check := exactlyOneArg("a silence id")
	require.NoError(t, check(cmd, []string{"sil-1"}))

	err := check(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "a silence id", "names the missing operand")
	require.Contains(t, err.Error(), "a10r silences get <id>", "shows the usage line")
	require.NotContains(t, err.Error(), "arg(s)", "not cobra's bare count message")

	require.Error(t, check(cmd, []string{"a", "b"}), "too many args also rejected")
}

func TestAtLeastOneArg(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "expire <id> [<id>...]"}
	check := atLeastOneArg("silence id")
	require.NoError(t, check(cmd, []string{"a"}))
	require.NoError(t, check(cmd, []string{"a", "b"}))

	err := check(cmd, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one silence id")
}

// TestArgErrors_RealCommandTree locks the user-facing message end to
// end: through the assembled root (where UseLine carries the full
// `a10r silences get` path) and with no `execute:` wrap from Execute.
func TestArgErrors_RealCommandTree(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"silences", "get"}, want: "requires a silence id (usage: a10r silences get <id> [flags])"},
		{args: []string{"alerts", "get"}, want: "requires an alert fingerprint (usage: a10r alerts get <fingerprint> [flags])"},
		{args: []string{"silences", "expire"}, want: "requires at least one silence id (usage: a10r silences expire <id> [<id>...] [flags])"},
	}
	for _, tc := range cases {
		t.Run(tc.args[1]+" "+tc.args[0], func(t *testing.T) {
			t.Parallel()
			var flags GlobalFlags
			root := newRootCmd(&flags, noopRootRun)
			registerSubcommands(root, &flags)
			root.SetArgs(tc.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			err := root.Execute()
			require.EqualError(t, err, tc.want)
		})
	}
}

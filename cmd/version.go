// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newVersionCmd returns the `a10r version` subcommand. Output is a
// single space-separated line so it composes cleanly in shell pipelines
// (e.g. `a10r version | awk '{print $2}'` to extract the version).
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printVersion(cmd.OutOrStdout())
		},
	}
}

// printVersion writes the version line to out. Pulled out of the cobra
// RunE closure so tests can drive it without spinning up a full cobra
// command.
func printVersion(out io.Writer) error {
	if _, err := fmt.Fprintf(out, "a10r %s commit=%s built=%s\n", version, commit, date); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	return nil
}

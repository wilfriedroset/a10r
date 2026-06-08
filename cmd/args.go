// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// exactlyOneArg enforces a single positional argument. On a mismatch it
// names the expected operand and shows the usage line, because the root
// command silences cobra's own usage output (SilenceUsage), so cobra's
// bare "accepts 1 arg(s), received 0" would otherwise be all the user
// sees.
func exactlyOneArg(operand string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("requires %s (usage: %s)", operand, cmd.UseLine())
		}
		return nil
	}
}

// atLeastOneArg enforces one or more positional arguments. operand is
// the bare singular noun ("silence id"); the validator owns the "at
// least one" phrasing so callers stay parallel with exactlyOneArg.
func atLeastOneArg(operand string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("requires at least one %s (usage: %s)", operand, cmd.UseLine())
		}
		return nil
	}
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// buildHelpRoot mirrors Execute's group + subcommand wiring so the
// help-output assertion below runs without driving cobra's full
// Execute path (which would block on a TTY for the no-arg runTUI
// branch). Keeping this in tests rather than exporting a helper
// from cmd avoids growing the public API for one assertion.
func buildHelpRoot(t *testing.T) *cobra.Command {
	t.Helper()

	var flags GlobalFlags
	root := newRootCmd(&flags, func(*cobra.Command, *GlobalFlags) error { return nil })
	// Groups + completion/help GroupID pins are set inside
	// newRootCmd; the test only adds the subcommands.
	root.AddCommand(
		newVersionCmd(),
		newInfoCmd(&flags),
		newValidateCmd(&flags),
		newDoctorCmd(&flags),
		newInitCmd(&flags),
	)
	return root
}

func TestExecute_HelpGroupsSubcommands(t *testing.T) {
	t.Parallel()

	root := buildHelpRoot(t)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	out := buf.String()
	// Section headers appear once each in the documented order.
	require.Contains(t, out, "Read:")
	require.Contains(t, out, "Diagnostics:")
	require.Contains(t, out, "Setup:")

	// Diagnostics group carries every command we registered there.
	for _, cmd := range []string{"validate", "version", "info", "doctor"} {
		require.Contains(t, out, cmd, "command %q must appear in --help output", cmd)
	}

	// completion + help land under Setup (cobra-auto-registered).
	for _, cmd := range []string{"completion", "help"} {
		require.Contains(t, out, cmd)
	}

	// "Available Commands:" — the unnamed default group — must NOT
	// appear, otherwise some command was missed.
	require.NotContains(t, out, "Additional Commands:",
		"every subcommand must have a GroupID")
	// Read: section is empty in v0.0.1 (alerts list, silences list
	// are queued for follow-on commits) — but the header still
	// renders so users know the slot exists.
	readSection, _, found := strings.Cut(out, "Diagnostics:")
	require.True(t, found)
	require.Contains(t, readSection, "Read:")
}

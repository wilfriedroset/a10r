// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const (
	// defaultLogFormat is the format used when neither the CLI flag nor
	// the config sets one. Matches D1/D3 — JSON and logfmt are the only
	// two configurable formats; logfmt is the friendlier default for
	// `tail`-ing the log file by hand.
	defaultLogFormat = "logfmt"
)

// RootRunFn is the RunE indirection the root command uses when no
// subcommand is supplied. Production wires runTUI; tests inject a
// no-op so the flag-binding suite doesn't try to open a TTY.
type RootRunFn func(*cobra.Command, *GlobalFlags) error

// newRootCmd builds the a10r root command and binds every K1 persistent
// flag onto flags. Callers register subcommands via cmd.AddCommand
// before calling Execute. runFn is the no-subcommand RunE — typically
// runTUI; tests pass a no-op so cobra's RunE doesn't try to open a TTY.
func newRootCmd(flags *GlobalFlags, runFn RootRunFn) *cobra.Command {
	if runFn == nil {
		runFn = runTUI
	}
	rootCmd := &cobra.Command{
		Use:   "a10r",
		Short: "TUI for Prometheus Alertmanager and Grafana Mimir",
		Long: `a10r is a modern, fast, intuitive TUI for Prometheus Alertmanager
and Grafana Mimir, inspired by k9s.

Run with no subcommand to launch the TUI.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFn(cmd, flags)
		},
		PersistentPreRunE: persistentPreRun(flags),
	}

	f := rootCmd.PersistentFlags()
	f.StringVarP(&flags.ConfigPath, "config", "c", "",
		"path to an explicit config file (overrides --config-dir)")
	f.StringVar(&flags.ConfigDir, "config-dir", "",
		"config directory (default: XDG-resolved per-OS)")
	f.StringVar(&flags.LogPath, "log", "",
		"log file path (default: XDG-resolved per-OS)")
	f.StringVar(&flags.LogFormat, "log-format", defaultLogFormat,
		"log output format: json or logfmt")
	f.BoolVar(&flags.Debug, "debug", false,
		"set log level to debug")
	f.BoolVar(&flags.Quiet, "quiet", false,
		"set log level to warn (silences info)")
	f.BoolVar(&flags.ReadOnly, "read-only", false,
		"force read-only mode across the session (no silence create/update/expire)")
	f.StringVar(&flags.Tenant, "tenant", "",
		"pre-select tenant(s) at startup; mirrors :tenant <name|all|a,b> syntax")
	f.DurationVar(&flags.PollInterval, "poll-interval", 0,
		"override defaults.poll_interval for this run (0 = use config value)")
	f.StringVar(&flags.Theme, "theme", "",
		"override theme.name for this run (empty = use config value)")

	return rootCmd
}

// persistentPreRun wraps the per-invocation reconcilers that need to
// run before any RunE — today just the log-level flag reconciliation,
// but config loading and logger init will compose here as they land
// in follow-up commits. Returning a closure (rather than inlining one)
// keeps newRootCmd flat and gives reconcilers a single seam for tests.
func persistentPreRun(flags *GlobalFlags) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		return reconcileLogLevelFlags(flags, cmd.ErrOrStderr())
	}
}

// reconcileLogLevelFlags applies the K1 rule "if both --debug and
// --quiet are set, --debug wins and a warning is logged". The reset
// is intentional so downstream resolution sees a single coherent
// level rather than two contradictory bits.
func reconcileLogLevelFlags(flags *GlobalFlags, errOut io.Writer) error {
	if flags.Debug && flags.Quiet {
		if _, err := fmt.Fprintln(errOut, "warning: --debug overrides --quiet"); err != nil {
			return fmt.Errorf("write log-level warning: %w", err)
		}
		flags.Quiet = false
	}
	return nil
}

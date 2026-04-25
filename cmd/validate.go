// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/config"
)

// newValidateCmd returns the `a10r validate` subcommand. Loads the
// config, surfaces parse / interpolation / unknown-field errors with
// a non-zero exit, and reports a one-line summary on success. Useful
// for CI/CD pipelines that template a10r.yaml.
//
// Optional positional arg overrides the loader's directory-based
// resolution: `a10r validate /etc/a10r/staging.yaml` validates that
// specific file regardless of --config-dir / A10R_CONFIG_DIR.
func newValidateCmd(flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate [path/to/a10r.yaml]",
		Short: "Parse the config file and report errors",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd.OutOrStdout(), flags, args)
		},
	}
}

// runValidate is split out of the cobra closure so tests can drive it
// against a captured writer without invoking cobra's machinery.
func runValidate(out io.Writer, flags *GlobalFlags, args []string) error {
	opts := loadOptsFromArgs(flags, args)
	cfg, err := config.Load(opts)
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	if _, err := fmt.Fprintf(out, "config valid: %d backend(s) configured\n", len(cfg.Backends)); err != nil {
		return fmt.Errorf("write validate output: %w", err)
	}
	return nil
}

// loadOptsFromArgs builds the LoadOpts from the optional positional
// arg (a single config file path, split into Dir+File) or, when
// absent, the --config-dir flag value.
func loadOptsFromArgs(flags *GlobalFlags, args []string) config.LoadOpts {
	if len(args) == 1 {
		return config.LoadOpts{
			Dir:  filepath.Dir(args[0]),
			File: filepath.Base(args[0]),
		}
	}
	return config.LoadOpts{Dir: flags.ConfigDir}
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/report"
	"github.com/wilfriedroset/a10r/internal/uistate"
	"github.com/wilfriedroset/a10r/internal/xdg"
)

// newInfoCmd returns the `a10r info` subcommand. Diagnostic output
// for "where is a10r looking for its config" — runs cleanly even
// when the config file does not exist; it just reports the current
// state.
func newInfoCmd(flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "info",
		Short:   "Print resolved config dir, log path, and configured backends",
		GroupID: groupDiag,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInfo(cmd.OutOrStdout(), flags)
		},
	}
}

// runInfo resolves the host-side context (config dir, log path,
// possibly-loaded config) before delegating to the pure renderer in
// internal/report, which the TUI's `:info` page also renders from.
func runInfo(out io.Writer, flags *GlobalFlags) error {
	configDir, err := config.ResolveDir(flags.ConfigDir)
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}

	cfg, loadErr := config.Load(loadOptsFromFlags(flags))
	if loadErr != nil && !errors.Is(loadErr, config.ErrNotFound) {
		return fmt.Errorf("load config: %w", loadErr)
	}

	// The precedence chain runs through config.Resolve rather than the
	// raw flags, so the report names the log file and the skin the
	// process would actually use — the same two values the TUI's
	// `:info` page resolves from the same file.
	fileCfg := config.Config{}
	if cfg != nil {
		fileCfg = *cfg
	}
	eff, err := config.Resolve(*flags, os.Getenv, fileCfg)
	if err != nil {
		return fmt.Errorf("resolve config: %w", err)
	}

	// Aliases are an optional overlay; a missing file is fine and
	// reports as zero. A malformed file is loud — the operator sees
	// the parse error here rather than at TUI startup.
	aliases, aliasErr := config.LoadAliases(configDir)
	if aliasErr != nil {
		return fmt.Errorf("load aliases: %w", aliasErr)
	}

	stateDir, rememberedScope := stateReport(cfg)

	return report.Info(out, report.InfoInput{ //nolint:wrapcheck // the only error is the caller's own io.Writer, already named by report.Info.
		Theme:      eff.Config.Theme.Name,
		Version:    version,
		Commit:     commit,
		Date:       date,
		ConfigDir:  configDir,
		LogPath:    log.ReportPath(eff.Config.Log.Path),
		Config:     cfg,
		NotFound:   errors.Is(loadErr, config.ErrNotFound),
		AliasCount: len(aliases),

		StateDir:        stateDir,
		RememberedScope: rememberedScope,
	})
}

// stateReport resolves the state directory and, when tui.remember is
// on, the tenant scope a10r would boot on. The stored scope is pruned
// against the configured backends exactly as boot prunes it, so info
// never names a tenant a10r would silently drop. An unresolvable
// directory reports empty rather than failing the command: info is a
// diagnostic, and a missing HOME is the very thing an operator runs
// it to find out.
func stateReport(cfg *config.Config) (dir, scope string) {
	dir, err := xdg.DefaultStateDir()
	if err != nil {
		return "", ""
	}
	if cfg == nil || !cfg.TUI.Remember {
		return dir, ""
	}
	store := uistate.Open(dir)
	defer func() { _ = store.Close() }()
	return dir, report.RememberedScope(cfg, store)
}

// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"log/slog"

	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/report"
	"github.com/wilfriedroset/a10r/internal/uistate"
)

// infoInputs are the startup facts the `:info` report needs that no
// other stage already hands around.
type infoInputs struct {
	deps       Deps
	cfg        *config.Config
	configDir  string
	aliasCount int
	found      bool
	store      *uistate.Store
}

// buildInfoReport returns the renderer the `:info` page calls on
// every frame it re-renders. It is a closure rather than a captured
// value because the remembered tenant scope changes during a session
// and the page must report what a10r would boot on next, not what it
// booted on this time.
func buildInfoReport(in infoInputs) func() report.InfoInput {
	stateDir, err := in.deps.HistoryDir()
	if err != nil {
		// Same degradation as `a10r info`: a host without a resolvable
		// state dir drops the line rather than failing the report.
		slog.Debug("no state dir for the info report", slog.Any("err", err))
		stateDir = ""
	}
	logPath := resolvedLogPath(in.cfg.Log.Path)

	return func() report.InfoInput {
		return report.InfoInput{
			Version:         in.deps.Version,
			Commit:          in.deps.Commit,
			Date:            in.deps.Date,
			ConfigDir:       in.configDir,
			LogPath:         logPath,
			Config:          in.cfg,
			NotFound:        !in.found,
			AliasCount:      in.aliasCount,
			StateDir:        stateDir,
			RememberedScope: report.RememberedScope(in.cfg, in.store),
			Theme:           in.cfg.Theme.Name,
		}
	}
}

// resolvedLogPath names the file the sink actually opens, so the
// report cannot print an empty path for the defaulted case. A host
// where even the default does not resolve reports the sentinel
// instead: `a10r info` fails loudly there, but the TUI is already
// running and a bare `log path:` label answers nothing.
func resolvedLogPath(configured string) string {
	if configured != "" {
		return configured
	}
	path, err := a10rlog.DefaultPath()
	if err != nil {
		slog.Debug("no default log path for the info report", slog.Any("err", err))
		return "(unresolved)"
	}
	return path
}

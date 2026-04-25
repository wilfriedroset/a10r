// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/log"
)

// newInfoCmd returns the `a10r info` subcommand. Diagnostic output
// for "where is a10r looking for its config" — runs cleanly even
// when the config file does not exist (the wizard is the long-term
// answer for that case; info is just for telling the user what state
// they are in).
func newInfoCmd(flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Print resolved config dir, log path, and configured backends",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInfo(cmd.OutOrStdout(), flags)
		},
	}
}

// runInfo wires the cobra command to the renderInfo body, resolving
// the host-side context (config dir, log path, possibly-loaded
// config) before delegating to the pure renderer.
func runInfo(out io.Writer, flags *GlobalFlags) error {
	configDir, err := config.ResolveDir(flags.ConfigDir)
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}

	logPath := flags.LogPath
	if logPath == "" {
		resolved, perr := log.DefaultPath()
		if perr != nil {
			return fmt.Errorf("resolve log path: %w", perr)
		}
		logPath = resolved
	}

	cfg, loadErr := config.Load(config.LoadOpts{Dir: flags.ConfigDir})
	if loadErr != nil && !errors.Is(loadErr, config.ErrNotFound) {
		return fmt.Errorf("load config: %w", loadErr)
	}

	return renderInfo(out, infoContext{
		Version:   version,
		Commit:    commit,
		Date:      date,
		ConfigDir: configDir,
		LogPath:   logPath,
		Config:    cfg,
		NotFound:  errors.Is(loadErr, config.ErrNotFound),
	})
}

// infoContext is the deterministic input renderInfo consumes. Pulled
// out so the test injects fixed strings (version="dev", commit="test"
// etc.) and the golden file matches byte-for-byte across hosts.
type infoContext struct {
	Version   string
	Commit    string
	Date      string
	ConfigDir string
	LogPath   string
	Config    *config.Config // nil when NotFound is true
	NotFound  bool
}

// renderInfo writes the human-readable info report to out. Format
// is pinned by cmd/testdata/info_*.golden so a regression in
// formatting is loud.
func renderInfo(out io.Writer, ctx infoContext) error {
	w := &writer{out: out}
	w.printf("a10r %s commit=%s built=%s\n\n", ctx.Version, ctx.Commit, ctx.Date)
	w.printf("config dir: %s\n", ctx.ConfigDir)
	w.printf("log path:   %s\n", ctx.LogPath)

	if ctx.NotFound {
		w.printf("\nconfig: not found (run `a10r` with no subcommand to launch the first-run wizard)\n")
		return w.err
	}
	if ctx.Config == nil {
		return w.err
	}

	w.printf("\nbackends (%d):\n", len(ctx.Config.Backends))
	for _, b := range ctx.Config.Backends {
		renderBackend(w, b)
	}
	return w.err
}

// writer is a small fmt.Fprintf wrapper that captures the first
// error and short-circuits subsequent calls. Lets the renderers
// stay flat (no `if err != nil { return err }` after every line)
// without growing nolint directives that gocritic.whyNoLint would
// then flag for missing explanations.
type writer struct {
	out io.Writer
	err error
}

func (w *writer) printf(format string, args ...any) {
	if w.err != nil {
		return
	}
	if _, err := fmt.Fprintf(w.out, format, args...); err != nil {
		w.err = fmt.Errorf("write info output: %w", err)
	}
}

func renderBackend(w *writer, b config.Backend) {
	w.printf("  %s\n", b.Name)
	w.printf("    url:    %s\n", b.URL)
	if b.Prefix != "" {
		w.printf("    prefix: %s\n", b.Prefix)
	}
	if b.Tenant != "" {
		header := b.TenantHeader
		if header == "" {
			header = "(no header)"
		}
		w.printf("    tenant: %s (%s)\n", b.Tenant, header)
	}
	if authLabel := authTypeLabel(b.Auth); authLabel != "" {
		w.printf("    auth:   %s\n", authLabel)
	}
	if caps := capabilityList(b.Capabilities); caps != "" {
		w.printf("    caps:   %s\n", caps)
	}
}

// authTypeLabel summarises the configured auth as a single word for
// the info report. Returns empty string when no auth is configured —
// the caller skips the line entirely.
func authTypeLabel(spec *config.AuthSpec) string {
	if spec == nil || spec.Type == "" {
		return ""
	}
	return spec.Type
}

// capabilityList returns the enabled capability flags as a comma-
// separated label. Empty means no capabilities are enabled and the
// caller skips the line.
func capabilityList(caps config.Capabilities) string {
	var enabled []string
	if caps.ConfigAPI {
		enabled = append(enabled, "config_api")
	}
	if caps.TenantAdmin {
		enabled = append(enabled, "tenant_admin")
	}
	if caps.Ring {
		enabled = append(enabled, "ring")
	}
	return strings.Join(enabled, ", ")
}

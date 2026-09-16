// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/log"
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

	cfg, loadErr := config.Load(loadOptsFromFlags(flags))
	if loadErr != nil && !errors.Is(loadErr, config.ErrNotFound) {
		return fmt.Errorf("load config: %w", loadErr)
	}

	// Aliases are an optional overlay; a missing file is fine and
	// reports as zero. A malformed file is loud — the operator sees
	// the parse error here rather than at TUI startup.
	aliases, aliasErr := config.LoadAliases(configDir)
	if aliasErr != nil {
		return fmt.Errorf("load aliases: %w", aliasErr)
	}

	var fileTheme string
	if cfg != nil {
		fileTheme = cfg.Theme.Name
	}

	stateDir, rememberedScope := stateReport(cfg)

	return renderInfo(out, infoContext{
		Theme:      config.ResolveTheme(flags.Theme, fileTheme),
		Version:    version,
		Commit:     commit,
		Date:       date,
		ConfigDir:  configDir,
		LogPath:    logPath,
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
	names := make([]string, len(cfg.Backends))
	for i, b := range cfg.Backends {
		names[i] = b.Name
	}
	pruned, _ := uistate.PruneScope(store.Scope(), names)
	if pruned == config.ScopeAll {
		return dir, ""
	}
	return dir, pruned
}

// infoContext is the deterministic input renderInfo consumes. Pulled
// out so the test injects fixed strings (version="dev", commit="test"
// etc.) and the golden file matches byte-for-byte across hosts.
type infoContext struct {
	Version    string
	Commit     string
	Date       string
	ConfigDir  string
	LogPath    string
	Config     *config.Config // nil when NotFound is true
	NotFound   bool
	AliasCount int // resolved <config-dir>/aliases.yaml entry count
	// StateDir is the parent of the prompt history files and
	// ui-state.yaml. Empty when it could not be resolved. The log
	// file only joins them on unix; macOS and Windows put logs
	// elsewhere.
	StateDir string
	// RememberedScope is the tenant scope ui-state.yaml holds, and is
	// empty both when tui.remember is off and when nothing is
	// remembered. Sort entries are not listed: open the file for those.
	RememberedScope string
	// Theme is the skin name after CLI-over-file precedence, so
	// `a10r info --theme X` reports the skin the TUI would use.
	Theme string
}

// renderInfo writes the human-readable info report to out. Format
// is pinned by cmd/testdata/info_*.golden so a regression in
// formatting is loud.
func renderInfo(out io.Writer, ctx infoContext) error {
	w := &writer{out: out}
	w.printf("a10r %s commit=%s built=%s\n\n", ctx.Version, ctx.Commit, ctx.Date)
	w.printf("config dir: %s\n", ctx.ConfigDir)
	if ctx.StateDir != "" {
		w.printf("state dir:  %s\n", ctx.StateDir)
	}
	w.printf("log path:   %s\n", ctx.LogPath)
	w.printf("aliases:    %d\n", ctx.AliasCount)
	w.printf("theme:      %s\n", themeLabel(ctx.Theme))
	if ctx.RememberedScope != "" {
		w.printf("scope:      %s (remembered)\n", ctx.RememberedScope)
	}

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
	renderGuardrails(w, ctx.Config)
	return w.err
}

// renderGuardrails lists the write policy in config order, then the
// tenant globs that match no configured backend. The whole block is
// skipped when no rule exists so the common report stays short; an
// operator with no guardrails must not have to read a line telling
// them so.
func renderGuardrails(w *writer, cfg *config.Config) {
	if len(cfg.Guardrails) == 0 {
		return
	}
	w.printf("\nguardrails (%d):\n", len(cfg.Guardrails))
	for _, r := range cfg.Guardrails {
		w.printf("  %s\n", guardrailLine(r))
	}

	names := make([]string, len(cfg.Backends))
	for i, b := range cfg.Backends {
		names[i] = b.Name
	}
	for _, g := range cfg.Guardrails.UnmatchedTenants(names) {
		w.printf("  warning: tenant glob %q matches no configured backend\n", g)
	}
}

// guardrailLine renders one rule as a single line.
func guardrailLine(r guardrail.Rule) string {
	parts := []string{
		"tenants=" + globList(r.Tenants),
		"actions=" + globList(r.Actions),
	}
	if r.Deny {
		parts = append(parts, "deny")
	}
	if r.Confirmation != "" {
		parts = append(parts, "confirmation="+string(r.Confirmation))
	}
	if r.MaxBulk > 0 {
		parts = append(parts, fmt.Sprintf("max_bulk=%d", r.MaxBulk))
	}
	if r.Reason != "" {
		parts = append(parts, fmt.Sprintf("reason=%q", r.Reason))
	}
	return strings.Join(parts, "  ")
}

// globList renders an omitted glob list as the catch-all it means, so
// the report never leaves the reader guessing what an empty field
// matches.
func globList(globs []string) string {
	if len(globs) == 0 {
		return "*"
	}
	return strings.Join(globs, ",")
}

// themeLabel names the resolved skin for the info report. The auto
// sentinel resolves at TUI startup from the terminal background, so
// the label says so rather than naming a skin: info is headless and
// must never query the terminal to find out.
func themeLabel(name string) string {
	if name == "" {
		name = config.DefaultThemeName
	}
	if name == config.ThemeAuto {
		return name + " (terminal decides at start)"
	}
	return name
}

// writer is a small fmt.Fprintf wrapper that captures the first
// error and short-circuits subsequent calls. Lets the renderers
// stay flat instead of `if err != nil { return err }` after every
// line.
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
	if authLabel := authLabel(b); authLabel != "" {
		w.printf("    auth:   %s\n", authLabel)
	}
	if caps := capabilityList(b.Capabilities); caps != "" {
		w.printf("    caps:   %s\n", caps)
	}
}

// authLabel summarises the configured auth as a single word for the
// info report. Returns empty string when no auth is configured —
// the caller skips the line entirely. The schema's "at most one of
// basic_auth, authorization, bearer_token" rule (config.Backend.
// Validate) means at most one branch fires per backend.
func authLabel(b config.Backend) string {
	switch {
	case b.BasicAuth != nil:
		return authModeBasic
	case b.Authorization != nil:
		// authorization.type defaults to "Bearer" via Backend.Validate
		// — surface it as-is so the operator can read off the wire
		// scheme without consulting the source YAML.
		return "authorization (" + b.Authorization.Type + ")"
	case b.BearerToken != "":
		return "bearer"
	default:
		return ""
	}
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

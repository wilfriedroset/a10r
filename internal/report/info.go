// SPDX-License-Identifier: Apache-2.0

// Package report renders the diagnostic reports a10r shows about
// itself. The renderer lives outside `cmd` so `a10r info` on the
// terminal and the TUI's `:info` page can share one implementation
// and never drift apart.
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
)

// InfoInput is the deterministic input Info consumes. Pulled
// out so the test injects fixed strings (version="dev", commit="test"
// etc.) and the golden file matches byte-for-byte across hosts.
type InfoInput struct {
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
	// `a10r info --theme X` reports the skin the TUI would use. The
	// TUI passes the skin in force instead, never the auto sentinel.
	Theme string
}

// Info writes the human-readable info report to out. Format is
// pinned by internal/report/testdata/info_*.golden so a regression
// in formatting is loud.
func Info(out io.Writer, in InfoInput) error {
	w := &writer{out: out}
	w.printf("a10r %s commit=%s built=%s\n\n", in.Version, in.Commit, in.Date)
	w.printf("config dir: %s\n", in.ConfigDir)
	if in.StateDir != "" {
		w.printf("state dir:  %s\n", in.StateDir)
	}
	w.printf("log path:   %s\n", in.LogPath)
	w.printf("aliases:    %d\n", in.AliasCount)
	w.printf("theme:      %s\n", themeLabel(in.Theme))
	if in.RememberedScope != "" {
		w.printf("scope:      %s (remembered)\n", in.RememberedScope)
	}

	if in.NotFound {
		w.printf("\nconfig: not found (run `a10r` with no subcommand to launch the first-run wizard)\n")
		return w.err
	}
	if in.Config == nil {
		return w.err
	}

	w.printf("\nbackends (%d):\n", len(in.Config.Backends))
	for _, b := range in.Config.Backends {
		renderBackend(w, b)
	}
	renderGuardrails(w, in.Config)
	renderNotify(w, in.Config.TUI.Notify)
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
		w.printf("  warning: %s\n", guardrail.UnmatchedTenantWarning(g))
	}
}

// renderNotify reports the resolved notify settings, because notify is
// the one setting that makes a10r ring a terminal bell and run a
// subprocess, and an operator who hears a bell has one place to look.
func renderNotify(w *writer, n config.Notify) {
	if !n.Enabled {
		return
	}
	w.printf("\nnotify:\n")
	w.printf("  desktop:      %s\n", n.DesktopOrDefault())
	w.printf("  min_severity: %s\n", n.MinSeverityOrDefault())
	w.printf("  bell:         %s\n", onOff(n.BellOrDefault()))
	if len(n.Command) > 0 {
		w.printf("  command:      %s\n", commandLabel(n.Command))
	}
}

// commandLabel names the notify program and counts the arguments it
// withholds. The arguments can carry a webhook URL or an API token,
// and this is a report an operator pastes into an issue, so they are
// withheld the way authLabel withholds a backend credential.
func commandLabel(argv []string) string {
	switch len(argv) {
	case 1:
		return argv[0]
	case 2:
		return argv[0] + " (+1 arg)"
	default:
		return fmt.Sprintf("%s (+%d args)", argv[0], len(argv)-1)
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
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
	if r.MaxBulk != nil {
		parts = append(parts, fmt.Sprintf("max_bulk=%d", *r.MaxBulk))
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
	w.printf("    url:    %s\n", config.RedactURL(b.URL))
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
//
// The url branch is last on purpose. The url line above is redacted,
// so without it a backend that authenticates through userinfo would
// read as one that does not authenticate at all.
func authLabel(b config.Backend) string {
	switch {
	case b.BasicAuth != nil:
		return "basic"
	case b.Authorization != nil:
		// authorization.type defaults to "Bearer" via Backend.Validate
		// — surface it as-is so the operator can read off the wire
		// scheme without consulting the source YAML.
		return "authorization (" + b.Authorization.Type + ")"
	case b.BearerToken != "":
		return "bearer"
	case config.RedactURL(b.URL) != b.URL:
		return "url userinfo"
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

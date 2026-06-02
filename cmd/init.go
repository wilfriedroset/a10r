// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/wizard"
)

// authMode constants, shared by the wizard and the --kv parser.
const (
	authModeNone   = "none"
	authModeBearer = "bearer"
	authModeBasic  = "basic"
)

// Accepted-value lists, shared by the wizard prompt and the --kv validator.
var (
	validInitAuthModes = []string{authModeNone, authModeBearer, authModeBasic}
	validInitThemes    = []string{
		"catppuccin-mocha",
		"catppuccin-latte",
		themeGruvboxDark,
	}
)

// Prompt defaults, shared with the --kv path so matching inputs produce
// byte-identical YAML. Poll stays "30s" (not config.DefaultPollInterval, 1m)
// because the starter-config wizard has always offered 30s.
const (
	defaultPollInterval = "30s"
	defaultTheme        = "catppuccin-mocha"
)

// recognised --kv keys, sorted so the "unknown key" error echo is stable.
// No `kind` key per ADR 0039; prefix/tenant map straight to YAML fields.
var recognisedInitKeys = []string{
	"auth_mode", "basic_password", "basic_user", "bearer_token",
	fieldName, "poll_interval", "prefix", fieldTenant, fieldTheme, fieldURL,
}

// newInitCmd returns the `a10r init` subcommand (prompts per ADR 0039, or a
// headless --one-shot --kv flow). --kv without --one-shot fails closed so
// prompt defaults and kv overrides can't silently mix.
func newInitCmd(flags *GlobalFlags) *cobra.Command {
	var force bool
	var oneShot bool
	var dryRun bool
	var kvs []string
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Create a starter a10r.yaml via interactive prompts (or --one-shot --kv ...)",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(initIO{
				In:      cmd.InOrStdin(),
				Out:     cmd.OutOrStdout(),
				Err:     cmd.ErrOrStderr(),
				Flags:   flags,
				Force:   force,
				OneShot: oneShot,
				DryRun:  dryRun,
				KVs:     kvs,
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"overwrite an existing a10r.yaml without prompting")
	cmd.Flags().BoolVar(&oneShot, "one-shot", false,
		"skip every prompt and build the config from --kv pairs (headless)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the resulting YAML to stdout and exit without touching the filesystem")
	cmd.Flags().StringArrayVar(&kvs, "kv", nil,
		"key=value pair for one-shot mode (repeatable). Recognised keys: "+
			strings.Join(recognisedInitKeys, ", "))
	return cmd
}

// initIO bundles runInit's host handles so tests can inject readers/writers
// instead of os.Stdin / os.Stdout.
type initIO struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Flags   *GlobalFlags
	Force   bool
	OneShot bool
	// DryRun prints the resulting YAML to Out and returns without touching
	// the filesystem, under both the wizard and one-shot flows.
	DryRun bool
	KVs    []string
}

// runInit validates the flag combination then dispatches to the dry-run
// or write flow. Errors carry ExitConfigInvalid so CI can branch without
// parsing stderr.
func runInit(env initIO) error {
	if len(env.KVs) > 0 && !env.OneShot {
		return NewExitError(ExitConfigInvalid,
			errors.New("--kv requires --one-shot — pass both or neither"))
	}
	if env.DryRun {
		return runInitDryRun(env)
	}
	return runInitWrite(env)
}

// runInitDryRun emits the resolved YAML to env.Out without touching the
// filesystem. It short-circuits before path resolution and the overwrite
// guard so --dry-run works in a read-only/CI sandbox.
func runInitDryRun(env initIO) error {
	cfg, err := collectConfig(env)
	if err != nil {
		return err
	}
	if err := writeInitConfigTo(env.Out, cfg); err != nil {
		return NewExitError(ExitConfigInvalid, err)
	}
	return nil
}

// runInitWrite resolves the config path, refuses to clobber an existing
// file without --force, writes the collected config, and prints the
// post-write hints.
func runInitWrite(env initIO) error {
	dir, err := config.ResolveDir(env.Flags.ConfigDir)
	if err != nil {
		return NewExitError(ExitConfigInvalid, fmt.Errorf("resolve config dir: %w", err))
	}
	path := filepath.Join(dir, "a10r.yaml")

	if !env.Force {
		if _, err := os.Stat(path); err == nil {
			return NewExitError(ExitConfigInvalid,
				fmt.Errorf("refusing to overwrite %s — pass --force to confirm", path))
		}
	}

	cfg, err := collectConfig(env)
	if err != nil {
		return err
	}
	if err := writeInitConfig(path, cfg); err != nil {
		return NewExitError(ExitConfigInvalid, err)
	}

	fmt.Fprintf(env.Out, "wrote %s\n", path)
	printInitHints(env, cfg)
	return nil
}

// printInitHints emits the post-write nudges (Mimir setup, plaintext
// credential warning) to env.Err. Both skip silently when not applicable.
func printInitHints(env initIO, cfg config.Config) {
	if hint := mimirSetupHint(cfg.Backends[0].URL); hint != "" {
		fmt.Fprintln(env.Err, hint)
	}
	if hint := plaintextCredentialHint(cfg); hint != "" {
		fmt.Fprintln(env.Err, hint)
	}
}

// mimirSetupHint returns the post-write nudge from ADR 0039. The prefix half
// is suppressed when the URL already ends in `/alertmanager` so the hint
// can't contradict an explicit choice; the tenant half always prints.
func mimirSetupHint(urlStr string) string {
	var lines []string
	if !urlPathHasAlertmanagerSuffix(urlStr) {
		lines = append(lines,
			"NOTE: if your backend is Grafana Mimir or another multi-tenant Alertmanager front, "+
				"set prefix: /alertmanager in a10r.yaml (or include /alertmanager in the URL).")
	}
	lines = append(lines,
		"For multi-tenant Mimir, also set tenant_header: X-Scope-OrgID and tenant: <your-org>.",
		"See docs/end-users/configuration.md.")
	return strings.Join(lines, "\n")
}

// urlPathHasAlertmanagerSuffix reports whether the URL path ends with
// `/alertmanager` (trailing slash tolerated). Garbage URLs return false so
// the nudge still prints; worst case is one extra line.
func urlPathHasAlertmanagerSuffix(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/alertmanager")
}

// plaintextCredentialHint nudges when the config carries a literal credential
// rather than a `${VAR}` interpolation; empty string otherwise so the caller
// can Fprintln unconditionally. init is the only surface that writes
// credentials to disk, so the nudge belongs here.
func plaintextCredentialHint(cfg config.Config) string {
	for _, be := range cfg.Backends {
		if be.BearerToken != "" && !isEnvInterpolation(be.BearerToken) {
			return exportHintLine(be.Name, "TOKEN")
		}
		if be.BasicAuth != nil && be.BasicAuth.Password != "" &&
			!isEnvInterpolation(be.BasicAuth.Password) {
			return exportHintLine(be.Name, "PASSWORD")
		}
	}
	return ""
}

// exportHintLine builds the nudge string. suffix matches the env-var name to
// the credential kind so a bearer token isn't suggested as `_PASSWORD`.
func exportHintLine(backendName, suffix string) string {
	name := strings.ToUpper(backendName)
	return "NOTE: credentials stored in plaintext. To use env-var interpolation instead, " +
		"replace the value with ${A10R_BACKEND_" + name + "_" + suffix + "} (or any other name) " +
		"and export that variable. See docs."
}

// isEnvInterpolation reports whether s is a single `${VAR}` placeholder.
// Conservative: a value mixing a placeholder with plaintext (`prefix-${TOKEN}`)
// counts as plaintext for the nudge, because the prefix leak is still a leak.
func isEnvInterpolation(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "${") || !strings.HasSuffix(t, "}") {
		return false
	}
	// Reject embedded `${`/`}` so concatenated placeholders count as plaintext.
	inner := t[2 : len(t)-1]
	return !strings.Contains(inner, "${") && !strings.Contains(inner, "}")
}

// collectConfig dispatches between the wizard and the one-shot path; both
// funnel through buildInitConfig so matching inputs yield identical YAML.
func collectConfig(env initIO) (config.Config, error) {
	if env.OneShot {
		answers, err := parseKVAnswers(env.KVs)
		if err != nil {
			return config.Config{}, NewExitError(ExitConfigInvalid,
				fmt.Errorf("init --one-shot: %w", err))
		}
		cfg, err := buildInitConfig(answers)
		if err != nil {
			return config.Config{}, NewExitError(ExitConfigInvalid,
				fmt.Errorf("init --one-shot: %w", err))
		}
		return cfg, nil
	}
	cfg, err := promptConfig(env.In, env.Out)
	if err != nil {
		return config.Config{}, fmt.Errorf("init wizard: %w", err)
	}
	return cfg, nil
}

// initAnswers is the payload both flows produce and buildInitConfig consumes.
// PrefixSet / TenantSet distinguish `--kv tenant=` (explicitly empty) from an
// omitted key; only the one-shot path flips them (the wizard never prompts,
// per ADR 0039).
type initAnswers struct {
	Name      string
	URL       string
	Prefix    string
	PrefixSet bool
	Tenant    string
	TenantSet bool
	AuthMode  string
	Bearer    string
	BasicUser string
	BasicPass string
	Poll      string
	Theme     string
}

// promptConfig walks the prompt sequence into an initAnswers and hands off to
// buildInitConfig. Pure logic over wizard so tests drive it with a reader.
func promptConfig(in io.Reader, out io.Writer) (config.Config, error) {
	p := wizard.From(in, out)

	var ans initAnswers
	name, err := p.String("backend name", "prod", validateBackendName)
	if err != nil {
		return config.Config{}, fmt.Errorf("prompt backend name: %w", err)
	}
	ans.Name = name

	urlStr, err := p.String("backend URL", "", validateURL)
	if err != nil {
		return config.Config{}, fmt.Errorf("prompt backend URL: %w", err)
	}
	ans.URL = urlStr

	authMode, err := p.Choice("authentication", validInitAuthModes, authModeNone)
	if err != nil {
		return config.Config{}, fmt.Errorf("prompt auth mode: %w", err)
	}
	ans.AuthMode = authMode
	switch authMode {
	case authModeBearer:
		token, err := p.Secret("bearer token")
		if err != nil {
			return config.Config{}, fmt.Errorf("prompt bearer token: %w", err)
		}
		ans.Bearer = token
	case authModeBasic:
		user, err := p.String("username", "", nonEmpty("username"))
		if err != nil {
			return config.Config{}, fmt.Errorf("prompt username: %w", err)
		}
		ans.BasicUser = user
		pass, err := p.Secret("password")
		if err != nil {
			return config.Config{}, fmt.Errorf("prompt password: %w", err)
		}
		ans.BasicPass = pass
	}

	poll, err := p.String("default poll interval", defaultPollInterval, validateDuration)
	if err != nil {
		return config.Config{}, fmt.Errorf("prompt poll interval: %w", err)
	}
	ans.Poll = poll

	theme, err := p.Choice(fieldTheme, validInitThemes, defaultTheme)
	if err != nil {
		return config.Config{}, fmt.Errorf("prompt theme: %w", err)
	}
	ans.Theme = theme

	return buildInitConfig(ans)
}

// buildInitConfig is the single place answers turn into a Config, so both
// flows emit byte-identical YAML for matching inputs. Cross-field validation
// lives here (not in parseKVAnswers) to stay defensive against wizard drift.
// Per ADR 0039 prefix/tenant pass straight through; an explicit empty tenant
// keeps the header unset, else Mimir would 400 every request.
func buildInitConfig(ans initAnswers) (config.Config, error) {
	if err := validateInitAnswers(ans); err != nil {
		return config.Config{}, err
	}

	be := config.Backend{Name: ans.Name, URL: ans.URL}
	if ans.PrefixSet {
		be.Prefix = ans.Prefix
	}
	if ans.Tenant != "" {
		be.TenantHeader = "X-Scope-OrgID"
		be.Tenant = ans.Tenant
	}

	switch ans.AuthMode {
	case authModeBearer:
		be.BearerToken = ans.Bearer
	case authModeBasic:
		be.BasicAuth = &config.BasicAuth{Username: ans.BasicUser, Password: ans.BasicPass}
	}

	pollStr := ans.Poll
	if pollStr == "" {
		pollStr = defaultPollInterval
	}
	pollInterval, err := time.ParseDuration(pollStr)
	if err != nil {
		return config.Config{}, fmt.Errorf("poll_interval %q: %w", pollStr, err)
	}

	theme := ans.Theme
	if theme == "" {
		theme = defaultTheme
	}

	return config.Config{
		Backends: []config.Backend{be},
		Defaults: config.Defaults{PollInterval: pollInterval},
		Theme:    config.Theme{Name: theme},
	}, nil
}

// validateInitAnswers enforces the cross-field rules (required fields, auth
// mode + credentials, theme). It lives where both paths reach because
// one-shot mode bypasses the wizard's prompt-time checks.
func validateInitAnswers(ans initAnswers) error {
	if err := validateRequired(ans); err != nil {
		return err
	}
	if err := validateBackendName(ans.Name); err != nil {
		return err
	}
	if err := validateURL(ans.URL); err != nil {
		return err
	}
	if err := validateInitAuth(ans); err != nil {
		return err
	}
	if ans.Theme != "" && !slices.Contains(validInitThemes, ans.Theme) {
		return fmt.Errorf("theme %q: must be one of %s",
			ans.Theme, strings.Join(validInitThemes, ", "))
	}
	if ans.Poll != "" {
		if err := validateDuration(ans.Poll); err != nil {
			return err
		}
	}
	return nil
}

// validateRequired errors listing any absent required key (name, url).
func validateRequired(ans initAnswers) error {
	var missing []string
	if ans.Name == "" {
		missing = append(missing, fieldName)
	}
	if ans.URL == "" {
		missing = append(missing, fieldURL)
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required key(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// validateInitAuth checks auth-mode / credential consistency: each mode
// requires its own credentials and forbids the others'.
//
//nolint:gocognit,cyclop // the per-mode rules read clearest as one switch — it is the auth-model spec; splitting each case into a helper fragments that spec without simplifying it
func validateInitAuth(ans initAnswers) error {
	mode := ans.AuthMode
	if mode == "" {
		mode = authModeNone
	}
	switch mode {
	case authModeNone:
		if ans.Bearer != "" || ans.BasicUser != "" || ans.BasicPass != "" {
			return errors.New("auth_mode=none forbids bearer_token, basic_user, basic_password")
		}
	case authModeBearer:
		if ans.Bearer == "" {
			return errors.New("auth_mode=bearer requires bearer_token")
		}
		if ans.BasicUser != "" || ans.BasicPass != "" {
			return errors.New("auth_mode=bearer forbids basic_user, basic_password")
		}
	case authModeBasic:
		if ans.BasicUser == "" || ans.BasicPass == "" {
			return errors.New("auth_mode=basic requires both basic_user and basic_password")
		}
		if ans.Bearer != "" {
			return errors.New("auth_mode=basic forbids bearer_token")
		}
	default:
		return fmt.Errorf("auth_mode %q: must be one of %s",
			mode, strings.Join(validInitAuthModes, ", "))
	}
	return nil
}

// parseKVAnswers turns repeated `--kv key=value` flags into an initAnswers.
// Last write wins (matching cobra's StringArrayVar); unknown keys fail closed.
func parseKVAnswers(kvs []string) (initAnswers, error) {
	var ans initAnswers
	for _, kv := range kvs {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return initAnswers{}, fmt.Errorf("malformed --kv %q: expected key=value", kv)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return initAnswers{}, fmt.Errorf("malformed --kv %q: empty key", kv)
		}
		// value is NOT trimmed: a whitespace password is legal and `tenant=`
		// must round-trip as empty.
		if err := applyKVAnswer(&ans, key, value); err != nil {
			return initAnswers{}, err
		}
	}
	return ans, nil
}

// applyKVAnswer writes one key=value into ans, keeping the key list and field
// mapping in one place; the unknown-key error echoes the accepted set.
func applyKVAnswer(ans *initAnswers, key, value string) error {
	switch key {
	case fieldName:
		ans.Name = value
	case fieldURL:
		ans.URL = value
	case "prefix":
		ans.Prefix = value
		ans.PrefixSet = true
	case fieldTenant:
		ans.Tenant = value
		ans.TenantSet = true
	case "auth_mode":
		ans.AuthMode = value
	case "bearer_token":
		ans.Bearer = value
	case "basic_user":
		ans.BasicUser = value
	case "basic_password":
		ans.BasicPass = value
	case "poll_interval":
		ans.Poll = value
	case fieldTheme:
		ans.Theme = value
	default:
		return fmt.Errorf("unknown key %q: recognised keys are %s",
			key, strings.Join(recognisedInitKeys, ", "))
	}
	return nil
}

// writeInitConfig serialises cfg to path at 0o600 — the file may carry
// credentials, so it must not be world-readable. Creates parent dirs.
func writeInitConfig(path string, cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = f.Close() }()
	return writeInitConfigTo(f, cfg)
}

// writeInitConfigTo serialises cfg into w at 2-space indent. Shared by the
// real write and the --dry-run preview so the byte shape stays identical.
func writeInitConfigTo(w io.Writer, cfg config.Config) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close yaml encoder: %w", err)
	}
	return nil
}

func validateBackendName(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("name cannot be empty")
	}
	if len(s) > 64 {
		return errors.New("name too long (max 64 chars)")
	}
	return nil
}

func validateURL(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("URL cannot be empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return errors.New("URL must include scheme and host (e.g. https://am.example)")
	}
	return nil
}

func validateDuration(s string) error {
	if _, err := time.ParseDuration(s); err != nil {
		return fmt.Errorf("not a valid duration: %w", err)
	}
	return nil
}

func nonEmpty(field string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s cannot be empty", field)
		}
		return nil
	}
}

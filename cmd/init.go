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

// authMode constants. Shared by the interactive wizard and the
// --kv parser so the accepted strings stay in one place.
const (
	authModeNone   = "none"
	authModeBearer = "bearer"
	authModeBasic  = "basic"
)

// Canonical accepted-value lists. Shared between the wizard's
// Choice prompt and the one-shot --kv validator so adding an entry
// is a one-line change.
var (
	validInitAuthModes = []string{authModeNone, authModeBearer, authModeBasic}
	validInitThemes    = []string{
		"catppuccin-mocha",
		"catppuccin-latte",
		"gruvbox-dark",
	}
)

// defaultPollInterval is the wizard's prompt default — kept at
// "30s" rather than config.DefaultPollInterval (1m) because init
// writes a starter config and the wizard has always offered 30s.
// defaultTheme is the bundled theme the prompt suggests. Sharing
// both with the --kv path means a wizard run and a `--one-shot`
// run with the same explicit keys produce byte-identical YAML.
const (
	defaultPollInterval = "30s"
	defaultTheme        = "catppuccin-mocha"
)

// recognised --kv keys. The slice is iterated for the
// "unknown key" error so the message lists every accepted name.
// Sorted alphabetically so the error echo is reading-order stable
// without a sort at error time.
//
// Per ADR 0039 there is no `kind` key — the wizard no longer asks
// for backend kind and the one-shot path mirrors. `prefix:` and
// `tenant:` map directly to the YAML fields the loader consumes
// (no kind gate), so the operator passes them in standalone when
// the backend needs them.
var recognisedInitKeys = []string{
	"auth_mode", "basic_password", "basic_user", "bearer_token",
	"name", "poll_interval", "prefix", "tenant", "theme", "url",
}

// newInitCmd returns the `a10r init` subcommand. Walks the user
// through a small set of prompts (name, URL, auth, poll interval,
// theme — see ADR 0039) and writes the result to the resolved XDG
// config path.
//
// The `--force` flag overwrites an existing config file; without
// it the command refuses rather than silently clobbering the
// operator's hand-edited setup.
//
// The `--one-shot` / `--kv key=value` pair drives a headless flow:
// no prompts, the Config is built from the kv pairs directly. The
// pair is mandatory together — `--kv` without `--one-shot` fails
// closed because the operator either wants an interactive run or a
// fully scripted one; mixing the two would silently combine prompt
// defaults with kv overrides and obscure which side filled which
// field.
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

// initIO bundles the host-side handles runInit consumes so tests
// can inject a strings.Reader / bytes.Buffer pair without touching
// os.Stdin / os.Stdout.
type initIO struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Flags   *GlobalFlags
	Force   bool
	OneShot bool
	// DryRun, when true, makes runInit print the resulting YAML to
	// Out and return without touching the filesystem. Compatible with
	// both the interactive wizard (prompts still run, YAML lands on
	// stdout instead of disk) and `--one-shot --kv ...` (headless
	// preview a CI pipeline can capture before committing the file).
	DryRun bool
	KVs    []string
}

// runInit drives the chosen flow (interactive or one-shot) and
// writes the resulting YAML to the resolved config path. Wrapped
// errors carry an ExitConfigInvalid code so a caller's CI pipeline
// can branch on "init refused to write" without parsing stderr.
//
// --dry-run skips the path-resolve / existence check / write
// pipeline entirely: the resulting YAML lands on env.Out and the
// command returns. CI pipelines use this to preview a generated
// config before committing it; the headless complement to the
// wizard's "review before save" affordance.
func runInit(env initIO) error {
	if len(env.KVs) > 0 && !env.OneShot {
		return NewExitError(ExitConfigInvalid,
			errors.New("--kv requires --one-shot — pass both or neither"))
	}

	if env.DryRun {
		cfg, err := collectConfig(env)
		if err != nil {
			return err
		}
		if err := writeInitConfigTo(env.Out, cfg); err != nil {
			return NewExitError(ExitConfigInvalid, err)
		}
		return nil
	}

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
	if hint := mimirSetupHint(cfg.Backends[0].URL); hint != "" {
		fmt.Fprintln(env.Err, hint)
	}
	if hint := plaintextCredentialHint(cfg); hint != "" {
		fmt.Fprintln(env.Err, hint)
	}
	return nil
}

// mimirSetupHint returns the post-write discoverability nudge ADR
// 0039 wires in. The prefix half is suppressed when the URL path
// already ends with `/alertmanager` — the operator clearly engaged
// with the path themselves — so the hint does not contradict their
// explicit choice. The tenant half always prints because tenancy
// cannot be encoded in the URL. Empty string when the prefix half
// is suppressed AND no other lines remain (currently impossible
// since the tenant line is unconditional, kept as a guard for
// future toggles).
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
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// urlPathHasAlertmanagerSuffix reports whether the URL's path ends
// with `/alertmanager` (trailing slash tolerated). A nudge would
// duplicate guidance the operator's URL already encodes, so the
// hint suppresses the prefix half in this case. Garbage URLs fall
// through to false so the helpful nudge still prints — the worst
// outcome is one extra line.
func urlPathHasAlertmanagerSuffix(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/alertmanager")
}

// plaintextCredentialHint returns a one-line nudge when the rendered
// config carries a literal credential (basic password or bearer
// token) rather than a `${VAR}` interpolation. Empty string when the
// config is interpolation-only or auth-less, so the caller can
// fmt.Fprintln unconditionally without polluting the output stream.
//
// The CLI init flow is the only surface that writes credentials to
// disk, so this is where the nudge actually reaches a user.
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

// exportHintLine builds the operator-facing nudge string. Pure
// helper so tests can assert on the literal substring without
// reaching for the writer plumbing. The suffix lets the suggested
// env-var name match the credential kind — copy-pasting
// `_PASSWORD` for a bearer token would mislead operators following
// the hint.
func exportHintLine(backendName, suffix string) string {
	name := strings.ToUpper(backendName)
	return "NOTE: credentials stored in plaintext. To use env-var interpolation instead, " +
		"replace the value with ${A10R_BACKEND_" + name + "_" + suffix + "} (or any other name) " +
		"and export that variable. See docs."
}

// isEnvInterpolation reports whether s is a single `${VAR}` /
// `${VAR:-default}` placeholder — the shape config.interpolateBytes
// resolves at load time. Conservative: a value that *contains* a
// placeholder but also a plaintext segment (e.g. `prefix-${TOKEN}`)
// still counts as plaintext for the purposes of the nudge, because
// the prefix leak is a leak.
func isEnvInterpolation(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "${") || !strings.HasSuffix(t, "}") {
		return false
	}
	// Reject embedded `${` or `}` so `${A}${B}` (two placeholders
	// concatenated) and `${A}foo${B}` both fall through to the
	// plaintext branch.
	inner := t[2 : len(t)-1]
	return !strings.Contains(inner, "${") && !strings.Contains(inner, "}")
}

// collectConfig dispatches between the interactive wizard and the
// headless one-shot path. Both funnel through buildInitConfig so the
// generated YAML shape is identical for matching inputs.
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

// initAnswers is the canonical payload both flows produce — the
// wizard fills it from prompt input, the kv parser fills it from
// flag input. buildInitConfig consumes it.
//
// PrefixSet / TenantSet distinguish "explicitly empty" from "not
// supplied": an operator passing `--kv tenant=` (TenantSet=true,
// Tenant="") wants the slot deliberately blank, distinct from
// omitting `--kv tenant=` entirely. The wizard never prompts for
// these (ADR 0039) so PrefixSet / TenantSet are only flipped by
// the one-shot --kv path.
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

// promptConfig walks the user through the prompt sequence and
// returns the resulting Config. Pure logic on top of wizard so
// tests can drive it directly with a strings.Reader fixture.
//
// The function gathers the prompt answers into an initAnswers and
// hands off to buildInitConfig — that helper is the single place
// that turns gathered answers into a Config so the one-shot path
// (parseKVAnswers → buildInitConfig) emits a byte-identical YAML
// file for matching inputs.
func promptConfig(in io.Reader, out io.Writer) (config.Config, error) {
	p := wizard.From(in, out)

	var ans initAnswers
	name, err := p.String("backend name", "prod", validateBackendName)
	if err != nil {
		return config.Config{}, err
	}
	ans.Name = name

	urlStr, err := p.String("backend URL", "", validateURL)
	if err != nil {
		return config.Config{}, err
	}
	ans.URL = urlStr

	authMode, err := p.Choice("authentication", validInitAuthModes, authModeNone)
	if err != nil {
		return config.Config{}, err
	}
	ans.AuthMode = authMode
	switch authMode {
	case authModeBearer:
		token, err := p.Secret("bearer token")
		if err != nil {
			return config.Config{}, err
		}
		ans.Bearer = token
	case authModeBasic:
		user, err := p.String("username", "", nonEmpty("username"))
		if err != nil {
			return config.Config{}, err
		}
		ans.BasicUser = user
		pass, err := p.Secret("password")
		if err != nil {
			return config.Config{}, err
		}
		ans.BasicPass = pass
	}

	poll, err := p.String("default poll interval", defaultPollInterval, validateDuration)
	if err != nil {
		return config.Config{}, err
	}
	ans.Poll = poll

	theme, err := p.Choice("theme", validInitThemes, defaultTheme)
	if err != nil {
		return config.Config{}, err
	}
	ans.Theme = theme

	return buildInitConfig(ans)
}

// buildInitConfig is the single place gathered answers turn into a
// Config. Both promptConfig (interactive) and runInit's one-shot
// branch funnel through it so a wizard run and a `--kv ...` run
// with the same explicit inputs emit byte-identical YAML.
//
// Cross-field validation (auth mode requires its credential,
// parseable poll_interval) lives here rather than in parseKVAnswers
// because the wizard's prompt sequence already enforces these
// structurally — but the helper stays defensive so a future
// refactor of the wizard cannot drift from the kv path.
//
// Per ADR 0039, `prefix:` and `tenant:` pass straight through to
// the Backend without any wizard-time gate. An explicit empty
// tenant (TenantSet=true, Tenant="") keeps the tenant header
// unset, otherwise Mimir would 400 on every request.
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

// validateInitAnswers enforces the cross-field rules that turn a
// half-filled set of answers into a structurally consistent Config:
// required fields present, auth_mode in its allowed set, auth-mode-
// specific credentials present, theme in the allowed set.
//
// The wizard enforces the same rules at prompt time; one-shot mode
// hits the helper directly with a flat key=value bag, which is why
// the validation has to live somewhere both paths reach.
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

// validateRequired returns a precise "missing keys" error listing
// every required field absent from the answers. Required keys are
// name and url; everything else defaults or stays empty.
func validateRequired(ans initAnswers) error {
	var missing []string
	if ans.Name == "" {
		missing = append(missing, "name")
	}
	if ans.URL == "" {
		missing = append(missing, "url")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required key(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// validateInitAuth checks the auth-mode / credential consistency.
// none → no credentials may be set; bearer → bearer_token required,
// basic_user/basic_password forbidden; basic → both basic_user and
// basic_password required, bearer_token forbidden.
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

// parseKVAnswers turns repeated `--kv key=value` flags into an
// initAnswers. Last write wins (so `--kv name=foo --kv name=bar`
// yields name=bar, matching cobra's StringArrayVar semantics and
// the operator's natural reading order). Unknown keys fail closed
// with the recognised set echoed in the error.
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
		// value is NOT trimmed: a basic_password ending in
		// whitespace is legal, and a deliberate empty (`tenant=`)
		// must round-trip as empty.
		if err := applyKVAnswer(&ans, key, value); err != nil {
			return initAnswers{}, err
		}
	}
	return ans, nil
}

// applyKVAnswer writes one key=value into ans. Centralised so the
// recognised-key list and the field-mapping live in one place; the
// unknown-key error echoes the accepted set sorted for readability.
func applyKVAnswer(ans *initAnswers, key, value string) error {
	switch key {
	case "name":
		ans.Name = value
	case "url":
		ans.URL = value
	case "prefix":
		ans.Prefix = value
		ans.PrefixSet = true
	case "tenant":
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
	case "theme":
		ans.Theme = value
	default:
		return fmt.Errorf("unknown key %q: recognised keys are %s",
			key, strings.Join(recognisedInitKeys, ", "))
	}
	return nil
}

// writeInitConfig serialises cfg to path with 2-space indent
// (matching the file-side convention) and 0o600 permissions —
// the file may carry credentials, so it must not be world-
// readable. Creates intermediate directories.
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

// writeInitConfigTo serialises cfg into w with the same 2-space
// indent the file path uses. Shared between writeInitConfig (file
// destination, real run) and runInit's --dry-run branch (stdout
// destination, preview) so the byte shape stays identical.
func writeInitConfigTo(w io.Writer, cfg config.Config) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return enc.Close()
}

// validateBackendName rejects empty / whitespace / colliding
// names. Length cap matches the schema constraint.
func validateBackendName(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("name cannot be empty")
	}
	if len(s) > 64 {
		return errors.New("name too long (max 64 chars)")
	}
	return nil
}

// validateURL ensures the value parses as a URL with a scheme.
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

// validateDuration accepts every value time.ParseDuration would
// accept.
func validateDuration(s string) error {
	if _, err := time.ParseDuration(s); err != nil {
		return fmt.Errorf("not a valid duration: %w", err)
	}
	return nil
}

// nonEmpty returns a validator that rejects empty / whitespace
// input with a field-named error message.
func nonEmpty(field string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s cannot be empty", field)
		}
		return nil
	}
}

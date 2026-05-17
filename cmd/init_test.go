// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

// authMode constants for tests so goconst stays happy.
const (
	authBearer = "bearer"
	authBasic  = "basic"
)

// initInputs constructs the sequential lines runInit consumes, in
// the order the prompts fire. The mimir branch fires TWO extra
// prompts (prefix, then tenant); the prefix line uses the default
// (empty input → suggested value) unless the caller passes a
// non-default explicit value. Helper kept here so tests don't
// hand-build long string literals that drift when prompt order
// changes.
func initInputs(name, urlStr, kind, tenant, authMode, authA, authB, poll, theme string) string {
	parts := []string{name, urlStr, kind}
	if kind == "mimir" {
		// Two extra prompts in the mimir branch: the prefix
		// (empty input → suggested default) and the tenant ID.
		parts = append(parts, "", tenant)
	}
	parts = append(parts, authMode)
	switch authMode {
	case authBearer:
		parts = append(parts, authA)
	case authBasic:
		parts = append(parts, authA, authB)
	}
	parts = append(parts, poll, theme)
	return strings.Join(parts, "\n") + "\n"
}

func TestPromptConfig_AlertmanagerNoAuth(t *testing.T) {
	t.Parallel()

	in := initInputs(
		"prod", "https://am.example", "alertmanager", "",
		"none", "", "",
		"30s", "catppuccin-mocha",
	)
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "prod", cfg.Backends[0].Name)
	require.Equal(t, "https://am.example", cfg.Backends[0].URL)
	require.Empty(t, cfg.Backends[0].Prefix, "alertmanager kind leaves Prefix unset")
	require.Empty(t, cfg.Backends[0].TenantHeader)
	require.Empty(t, cfg.Backends[0].BearerToken)
	require.Nil(t, cfg.Backends[0].BasicAuth)
	require.Equal(t, "catppuccin-mocha", cfg.Theme.Name)
}

func TestPromptConfig_MimirAddsPrefixAndTenant(t *testing.T) {
	t.Parallel()

	in := initInputs(
		"mimir-prod", "https://mimir.example", "mimir", "tenant-a",
		"none", "", "",
		"60s", "catppuccin-latte",
	)
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "/alertmanager", cfg.Backends[0].Prefix)
	require.Equal(t, "X-Scope-OrgID", cfg.Backends[0].TenantHeader)
	require.Equal(t, "tenant-a", cfg.Backends[0].Tenant)
}

func TestSuggestedPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
		want string
	}{
		{name: "bare host", url: "https://mimir.example", want: "/alertmanager"},
		{name: "path-less with port", url: "https://mimir:9009", want: "/alertmanager"},
		{name: "path already includes alertmanager", url: "https://mimir/alertmanager", want: ""},
		{name: "trailing slash on alertmanager path", url: "https://mimir/alertmanager/", want: ""},
		{name: "deeper path with alertmanager suffix", url: "https://mimir/api/alertmanager", want: ""},
		{name: "unrelated path", url: "https://mimir/api/v1/foo", want: "/alertmanager"},
		{name: "garbage url falls through to default", url: "://broken", want: "/alertmanager"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, suggestedPrefix(tc.url))
		})
	}
}

func TestRunInit_MimirEmptyTenantPlusBasicAuthRoundTrips(t *testing.T) {
	t.Parallel()

	// End-to-end regression: the wizard sequence
	// {mimir, blank tenant, basic auth} must produce a YAML file
	// that loads back through config.Load without the
	// "tenant_header and tenant must be set together" validation
	// error — that combination is what surfaced the empty-tenant
	// bug.
	dir := t.TempDir()
	in := initInputs(
		"primary", "https://mimir.example", "mimir", "",
		authBasic, "alice", "hunter2",
		"30s", "catppuccin-mocha",
	)
	err := runInit(initIO{
		In:    strings.NewReader(in),
		Out:   &bytes.Buffer{},
		Err:   &bytes.Buffer{},
		Flags: &GlobalFlags{ConfigDir: dir},
	})
	require.NoError(t, err)

	loaded, err := config.Load(config.LoadOpts{Dir: dir})
	require.NoError(t, err, "generated config must round-trip cleanly through Load")
	require.Equal(t, "/alertmanager", loaded.Backends[0].Prefix)
	require.Empty(t, loaded.Backends[0].TenantHeader)
	require.Empty(t, loaded.Backends[0].Tenant)
	require.NotNil(t, loaded.Backends[0].BasicAuth)
	require.Equal(t, "alice", loaded.Backends[0].BasicAuth.Username)
}

func TestPromptConfig_BearerAuthFillsToken(t *testing.T) {
	t.Parallel()

	in := initInputs(
		"prod", "https://am.example", "alertmanager", "",
		authBearer, "supersecret", "",
		"30s", "catppuccin-mocha",
	)
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "supersecret", cfg.Backends[0].BearerToken)
}

func TestPromptConfig_BasicAuthFillsBoth(t *testing.T) {
	t.Parallel()

	in := initInputs(
		"prod", "https://am.example", "alertmanager", "",
		authBasic, "alice", "hunter2",
		"30s", "catppuccin-mocha",
	)
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.NotNil(t, cfg.Backends[0].BasicAuth)
	require.Equal(t, "alice", cfg.Backends[0].BasicAuth.Username)
	require.Equal(t, "hunter2", cfg.Backends[0].BasicAuth.Password)
}

func TestRunInit_RefusesOverwriteWithoutForce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a10r.yaml"), []byte("existing"), 0o600))

	flags := &GlobalFlags{ConfigDir: dir}
	err := runInit(initIO{
		In:    strings.NewReader(""),
		Out:   &bytes.Buffer{},
		Err:   &bytes.Buffer{},
		Flags: flags,
		Force: false,
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
	require.Contains(t, err.Error(), "refusing to overwrite")
}

func TestRunInit_WritesConfigAndRoundTrips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := initInputs(
		"prod", "https://am.example", "alertmanager", "",
		"none", "", "",
		"30s", "catppuccin-mocha",
	)

	var out bytes.Buffer
	err := runInit(initIO{
		In:    strings.NewReader(in),
		Out:   &out,
		Err:   &bytes.Buffer{},
		Flags: &GlobalFlags{ConfigDir: dir},
		Force: false,
	})
	require.NoError(t, err)
	path := filepath.Join(dir, "a10r.yaml")
	require.Contains(t, out.String(), "wrote ")
	require.Contains(t, out.String(), path)

	// File must round-trip through config.Load.
	loaded, err := config.Load(config.LoadOpts{Dir: dir})
	require.NoError(t, err)
	require.Len(t, loaded.Backends, 1)
	require.Equal(t, "prod", loaded.Backends[0].Name)

	// Permissions: 0o600 so a token-bearing config doesn't end up
	// world-readable.
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestValidateURL(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateURL("https://am.example"))
	require.NoError(t, validateURL("http://localhost:9093"))
	require.Error(t, validateURL(""))
	require.Error(t, validateURL("just-a-host"))
	require.Error(t, validateURL("scheme:nohost"))
}

func TestValidateBackendName(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateBackendName("prod"))
	require.Error(t, validateBackendName(""))
	require.Error(t, validateBackendName("   "))
	require.Error(t, validateBackendName(strings.Repeat("x", 65)))
}

func TestValidateDuration(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateDuration("30s"))
	require.NoError(t, validateDuration("1h30m"))
	require.Error(t, validateDuration(""))
	require.Error(t, validateDuration("forever"))
}

func TestNonEmpty(t *testing.T) {
	t.Parallel()

	v := nonEmpty("token")
	require.NoError(t, v("x"))
	err := v("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "token cannot be empty")
}

// minimal --kv set every one-shot run needs. Helpers compose on top
// via append(minKVs(), ...) so a test that exercises one knob
// doesn't repeat the boilerplate.
func minKVs() []string {
	return []string{
		"name=prod",
		"url=https://am.example",
		"kind=alertmanager",
	}
}

func TestParseKVAnswers_Minimal(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers(minKVs())
	require.NoError(t, err)
	require.Equal(t, "prod", ans.Name)
	require.Equal(t, "https://am.example", ans.URL)
	require.Equal(t, "alertmanager", ans.Kind)
	require.False(t, ans.PrefixSet)
	require.False(t, ans.TenantSet)
	require.Empty(t, ans.AuthMode)
}

func TestParseKVAnswers_LastWriteWins(t *testing.T) {
	t.Parallel()

	// Repeated --kv name=foo --kv name=bar must surface the second
	// value: cobra's StringArrayVar preserves order, and the operator
	// reading their own command line top-to-bottom expects the last
	// override to win.
	ans, err := parseKVAnswers([]string{
		"name=first", "name=second",
		"url=https://am.example", "kind=alertmanager",
	})
	require.NoError(t, err)
	require.Equal(t, "second", ans.Name)
}

func TestParseKVAnswers_EmptyValueIsRetained(t *testing.T) {
	t.Parallel()

	// `tenant=` is legal — single-tenant Mimir leaves the slot
	// blank — and must round-trip as an empty string with TenantSet
	// flipped so buildInitConfig can distinguish it from "user
	// didn't pass tenant at all".
	ans, err := parseKVAnswers([]string{
		"name=mimir", "url=https://mimir.example", "kind=mimir",
		"tenant=",
	})
	require.NoError(t, err)
	require.Empty(t, ans.Tenant)
	require.True(t, ans.TenantSet,
		"explicit empty value must still flip the *Set marker")
}

func TestParseKVAnswers_UnknownKeyFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := parseKVAnswers(append(minKVs(), "foo=bar"))
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown key "foo"`)
	require.Contains(t, err.Error(), "recognised keys are")
	// Echoes a few of the known keys so the operator gets a
	// pointer toward what they should have typed.
	require.Contains(t, err.Error(), "name")
	require.Contains(t, err.Error(), "url")
}

func TestParseKVAnswers_MalformedFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kv   string
		want string
	}{
		{name: "no equals", kv: "namefoo", want: "expected key=value"},
		{name: "empty key", kv: "=value", want: "empty key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseKVAnswers([]string{tc.kv})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestBuildInitConfig_RejectsMissingRequired(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kvs  []string
		want string // substring expected in the missing-keys error
	}{
		{name: "all missing", kvs: nil, want: "name, url, kind"},
		{name: "name missing", kvs: []string{"url=https://x", "kind=alertmanager"}, want: "name"},
		{name: "url missing", kvs: []string{"name=p", "kind=alertmanager"}, want: "url"},
		{name: "kind missing", kvs: []string{"name=p", "url=https://x"}, want: "kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ans, err := parseKVAnswers(tc.kvs)
			require.NoError(t, err)
			_, err = buildInitConfig(ans)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestBuildInitConfig_OneShotMatchesWizard(t *testing.T) {
	t.Parallel()

	// Same explicit inputs through both flows must produce the
	// same Config, byte-for-byte after YAML encoding. Locks the
	// "one helper, two paths" guarantee.
	wizardIn := initInputs(
		"prod", "https://am.example", "alertmanager", "",
		"none", "", "",
		"30s", "catppuccin-mocha",
	)
	wizardCfg, err := promptConfig(strings.NewReader(wizardIn), &bytes.Buffer{})
	require.NoError(t, err)

	ans, err := parseKVAnswers([]string{
		"name=prod",
		"url=https://am.example",
		"kind=alertmanager",
		"auth_mode=none",
		"poll_interval=30s",
		"theme=catppuccin-mocha",
	})
	require.NoError(t, err)
	kvCfg, err := buildInitConfig(ans)
	require.NoError(t, err)

	require.Equal(t, wizardCfg, kvCfg,
		"one-shot output must match wizard output for identical inputs")
}

func TestBuildInitConfig_MimirDefaultsPrefix(t *testing.T) {
	t.Parallel()

	// kind=mimir without an explicit prefix mirrors the wizard's
	// suggested-prefix behaviour: bare host gets /alertmanager.
	ans, err := parseKVAnswers([]string{
		"name=mimir", "url=https://mimir.example", "kind=mimir",
	})
	require.NoError(t, err)
	cfg, err := buildInitConfig(ans)
	require.NoError(t, err)
	require.Equal(t, "/alertmanager", cfg.Backends[0].Prefix)
}

func TestBuildInitConfig_MimirExplicitTenantSetsHeader(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers([]string{
		"name=mimir", "url=https://mimir.example", "kind=mimir",
		"tenant=tenant-a",
	})
	require.NoError(t, err)
	cfg, err := buildInitConfig(ans)
	require.NoError(t, err)
	require.Equal(t, "X-Scope-OrgID", cfg.Backends[0].TenantHeader)
	require.Equal(t, "tenant-a", cfg.Backends[0].Tenant)
}

func TestBuildInitConfig_MimirEmptyTenantOmitsHeader(t *testing.T) {
	t.Parallel()

	// Explicit empty tenant is the single-tenant Mimir case — the
	// header must NOT be injected, otherwise Mimir would 400 on
	// every request.
	ans, err := parseKVAnswers([]string{
		"name=mimir", "url=https://mimir.example", "kind=mimir",
		"tenant=",
	})
	require.NoError(t, err)
	cfg, err := buildInitConfig(ans)
	require.NoError(t, err)
	require.Empty(t, cfg.Backends[0].TenantHeader)
	require.Empty(t, cfg.Backends[0].Tenant)
}

func TestBuildInitConfig_RejectsPrefixWithAlertmanager(t *testing.T) {
	t.Parallel()

	// prefix is a mimir-only knob in the wizard; one-shot mirrors.
	ans, err := parseKVAnswers(append(minKVs(), "prefix=/alertmanager"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), "prefix is only valid with kind=mimir")
}

func TestBuildInitConfig_RejectsTenantWithAlertmanager(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers(append(minKVs(), "tenant=tenant-a"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tenant is only valid with kind=mimir")
}

func TestBuildInitConfig_RejectsBadKind(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers([]string{
		"name=p", "url=https://x", "kind=loki",
	})
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), `kind "loki"`)
}

func TestBuildInitConfig_BearerRequiresToken(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers(append(minKVs(), "auth_mode=bearer"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth_mode=bearer requires bearer_token")
}

func TestBuildInitConfig_BasicRequiresBoth(t *testing.T) {
	t.Parallel()

	// user-only and password-only together pin "both required" — a
	// regression to "only user required" fails user_only, a regression
	// to "only password required" fails password_only. The both-empty
	// case adds no catching power (every regression that breaks one
	// leg keeps both-empty erroring) so it stays cut.
	cases := []struct {
		name string
		kvs  []string
	}{
		{name: "user only", kvs: []string{"auth_mode=basic", "basic_user=alice"}},
		{name: "password only", kvs: []string{"auth_mode=basic", "basic_password=hunter2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ans, err := parseKVAnswers(append(minKVs(), tc.kvs...))
			require.NoError(t, err)
			_, err = buildInitConfig(ans)
			require.Error(t, err)
			require.Contains(t, err.Error(), "auth_mode=basic requires both basic_user and basic_password")
		})
	}
}

func TestBuildInitConfig_NoneRejectsCredentials(t *testing.T) {
	t.Parallel()

	// Default (unset) auth_mode is none. Setting bearer_token then
	// must surface the contradiction rather than silently dropping
	// the token on the floor.
	ans, err := parseKVAnswers(append(minKVs(), "bearer_token=secret"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth_mode=none forbids")
}

func TestBuildInitConfig_BadAuthMode(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers(append(minKVs(), "auth_mode=oauth"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), `auth_mode "oauth"`)
}

func TestBuildInitConfig_BadTheme(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers(append(minKVs(), "theme=solarized"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), `theme "solarized"`)
}

func TestBuildInitConfig_BadPollInterval(t *testing.T) {
	t.Parallel()

	ans, err := parseKVAnswers(append(minKVs(), "poll_interval=forever"))
	require.NoError(t, err)
	_, err = buildInitConfig(ans)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duration")
}

func TestBuildInitConfig_PollDefaultWhenOmitted(t *testing.T) {
	t.Parallel()

	// poll_interval omitted → 30s default (matches wizard prompt
	// default). Prevents a one-shot run from emitting a Config with
	// PollInterval zero, which would resolve to the package floor
	// rather than the user's expected 30s.
	ans, err := parseKVAnswers(minKVs())
	require.NoError(t, err)
	cfg, err := buildInitConfig(ans)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, cfg.Defaults.PollInterval)
	require.Equal(t, "catppuccin-mocha", cfg.Theme.Name)
}

func TestRunInit_OneShotWritesAndRoundTrips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := runInit(initIO{
		In:    strings.NewReader(""),
		Out:   &bytes.Buffer{},
		Err:   &bytes.Buffer{},
		Flags: &GlobalFlags{ConfigDir: dir},
		// No Force needed because the dir is empty.
		OneShot: true,
		KVs: []string{
			"name=prod",
			"url=https://am.example/api/v2",
			"kind=alertmanager",
			"auth_mode=basic",
			"basic_user=alice",
			"basic_password=hunter2",
			"poll_interval=45s",
			"theme=gruvbox-dark",
		},
	})
	require.NoError(t, err)

	loaded, err := config.Load(config.LoadOpts{Dir: dir})
	require.NoError(t, err, "one-shot output must round-trip through Load")
	require.Len(t, loaded.Backends, 1)
	require.Equal(t, "prod", loaded.Backends[0].Name)
	require.Equal(t, "https://am.example/api/v2", loaded.Backends[0].URL)
	require.NotNil(t, loaded.Backends[0].BasicAuth)
	require.Equal(t, "alice", loaded.Backends[0].BasicAuth.Username)
	require.Equal(t, "gruvbox-dark", loaded.Theme.Name)

	st, err := os.Stat(filepath.Join(dir, "a10r.yaml"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm(),
		"one-shot output must keep the same 0o600 perms as the wizard's")
}

func TestRunInit_KVWithoutOneShotFailsClosed(t *testing.T) {
	t.Parallel()

	// Mixing modes is a configuration smell — fail early, with a
	// clear message, before we touch the filesystem. Without this
	// guard the prompts would run with kvs ignored, silently.
	dir := t.TempDir()
	err := runInit(initIO{
		In:    strings.NewReader(""),
		Out:   &bytes.Buffer{},
		Err:   &bytes.Buffer{},
		Flags: &GlobalFlags{ConfigDir: dir},
		KVs:   []string{"name=prod"},
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
	require.Contains(t, err.Error(), "--kv requires --one-shot")
}

func TestRunInit_OneShotMissingRequiredFailsClosed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		// no --kv at all
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
	require.Contains(t, err.Error(), "missing required key")

	// File must NOT have been written: a partial-failure must not
	// leave a half-built a10r.yaml on disk.
	_, err = os.Stat(filepath.Join(dir, "a10r.yaml"))
	require.True(t, os.IsNotExist(err))
}

func TestRunInit_OneShotUnknownKeyFailsClosed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		KVs:     append(minKVs(), "color=blue"),
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
	require.Contains(t, err.Error(), `unknown key "color"`)
}

// TestRunInit_DryRunPrintsYAMLDoesNotWrite covers the QA-driven A3
// fix: a `--dry-run` invocation must surface the resulting YAML on
// stdout AND leave the filesystem untouched. The canonical headless-
// tool affordance for "show me what you'd do without doing it".
func TestRunInit_DryRunPrintsYAMLDoesNotWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out bytes.Buffer
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &out,
		Err:     &bytes.Buffer{},
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		DryRun:  true,
		KVs: []string{
			"name=prod",
			"url=https://am.example/api/v2",
			"kind=alertmanager",
			"auth_mode=none",
			"poll_interval=10s",
			"theme=catppuccin-mocha",
		},
	})
	require.NoError(t, err)

	// File must NOT exist on disk.
	_, statErr := os.Stat(filepath.Join(dir, "a10r.yaml"))
	require.True(t, os.IsNotExist(statErr),
		"--dry-run must not touch the filesystem (got stat err %v)", statErr)

	// stdout must carry the YAML body.
	body := out.String()
	require.Contains(t, body, "name: prod",
		"dry-run output must contain the configured backend name")
	require.Contains(t, body, "https://am.example/api/v2",
		"dry-run output must contain the configured URL")
}

// TestRunInit_DryRunDoesNotRequireForceOnExisting covers the
// "preview alongside an existing config" workflow: an operator
// inspects what `init` would produce without colliding with the
// hand-edited a10r.yaml already on disk.
func TestRunInit_DryRunDoesNotRequireForceOnExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Plant an existing config so the non-dry-run path would refuse.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "a10r.yaml"),
		[]byte("backends: []\n"), 0o600))

	var out bytes.Buffer
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &out,
		Err:     &bytes.Buffer{},
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		DryRun:  true,
		KVs:     minKVs(),
	})
	require.NoError(t, err,
		"--dry-run must succeed even when a10r.yaml already exists — no write happens")

	// Existing file must still be on disk unchanged.
	raw, readErr := os.ReadFile(filepath.Join(dir, "a10r.yaml"))
	require.NoError(t, readErr)
	require.Equal(t, "backends: []\n", string(raw),
		"existing config must be left untouched by --dry-run")
}

// TestRunInit_DryRunInvalidKVStillFails pins the validation path:
// `--dry-run` must NOT skip the cross-field rules — a config that
// would be rejected on write must also be rejected on preview, so
// CI pipelines that branch on the exit code work the same way.
func TestRunInit_DryRunInvalidKVStillFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		DryRun:  true,
		KVs:     []string{"name=prod"}, // missing url + kind
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
}

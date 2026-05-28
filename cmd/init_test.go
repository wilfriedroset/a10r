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
// the order the prompts fire (ADR 0039: name, URL, auth, [creds],
// poll, theme — no kind question and no mimir-only sub-prompts).
// The non-auth fields are pinned: every existing test uses the
// same backend name ("prod"), URL ("https://am.example"), poll
// ("30s"), and theme ("catppuccin-mocha"); a caller wanting a
// different fixture should hand-build the input string. Helper
// kept here so tests don't reimplement the prompt order each time.
func initInputs(authMode, authA, authB string) string {
	parts := []string{"prod", "https://am.example", authMode}
	switch authMode {
	case authBearer:
		parts = append(parts, authA)
	case authBasic:
		parts = append(parts, authA, authB)
	}
	parts = append(parts, "30s", "catppuccin-mocha")
	return strings.Join(parts, "\n") + "\n"
}

func TestPromptConfig_NoAuth(t *testing.T) {
	t.Parallel()

	in := initInputs("none", "", "")
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "prod", cfg.Backends[0].Name)
	require.Equal(t, "https://am.example", cfg.Backends[0].URL)
	require.Empty(t, cfg.Backends[0].Prefix,
		"wizard no longer prompts for prefix — slot stays empty unless --kv prefix= sets it")
	require.Empty(t, cfg.Backends[0].TenantHeader,
		"wizard no longer prompts for tenant — header stays empty unless --kv tenant= sets it")
	require.Empty(t, cfg.Backends[0].BearerToken)
	require.Nil(t, cfg.Backends[0].BasicAuth)
	require.Equal(t, "catppuccin-mocha", cfg.Theme.Name)
}

func TestPromptConfig_BearerAuthFillsToken(t *testing.T) {
	t.Parallel()

	in := initInputs(authBearer, "supersecret", "")
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "supersecret", cfg.Backends[0].BearerToken)
}

func TestPromptConfig_BasicAuthFillsBoth(t *testing.T) {
	t.Parallel()

	in := initInputs(authBasic, "alice", "hunter2")
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
	in := initInputs("none", "", "")

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

func TestValidators(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fn      func(string) error
		input   string
		wantErr bool
	}{
		{name: "url/https", fn: validateURL, input: "https://am.example"},
		{name: "url/http localhost", fn: validateURL, input: "http://localhost:9093"},
		{name: "url/empty", fn: validateURL, input: "", wantErr: true},
		{name: "url/no scheme", fn: validateURL, input: "just-a-host", wantErr: true},
		{name: "url/scheme without host", fn: validateURL, input: "scheme:nohost", wantErr: true},

		{name: "name/ok", fn: validateBackendName, input: "prod"},
		{name: "name/empty", fn: validateBackendName, input: "", wantErr: true},
		{name: "name/whitespace", fn: validateBackendName, input: "   ", wantErr: true},
		{name: "name/too long", fn: validateBackendName, input: strings.Repeat("x", 65), wantErr: true},

		{name: "duration/seconds", fn: validateDuration, input: "30s"},
		{name: "duration/compound", fn: validateDuration, input: "1h30m"},
		{name: "duration/empty", fn: validateDuration, input: "", wantErr: true},
		{name: "duration/garbage", fn: validateDuration, input: "forever", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.fn(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNonEmpty(t *testing.T) {
	t.Parallel()

	v := nonEmpty("token")
	require.NoError(t, v("x"))
	err := v("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "token cannot be empty")
}

// TestMimirSetupHint pins the post-write discoverability footer.
// Per ADR 0039: prefix half suppresses when the URL path already
// ends with `/alertmanager`; tenant half and doc anchor always
// print so the multi-tenant nudge reaches even URL-savvy operators.
func TestMimirSetupHint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		url         string
		wantMsgs    []string
		wantNotMsgs []string
	}{
		{
			name: "bare host fires both halves",
			url:  "https://mimir.example",
			wantMsgs: []string{
				"set prefix: /alertmanager",
				"tenant_header: X-Scope-OrgID",
				"docs/end-users/configuration.md",
			},
		},
		{
			name: "URL ends with /alertmanager suppresses prefix half",
			url:  "https://mimir.example/alertmanager",
			wantMsgs: []string{
				"tenant_header: X-Scope-OrgID",
				"docs/end-users/configuration.md",
			},
			wantNotMsgs: []string{"set prefix:"},
		},
		{
			name: "trailing slash on /alertmanager still suppresses prefix half",
			url:  "https://mimir.example/alertmanager/",
			wantMsgs: []string{
				"tenant_header: X-Scope-OrgID",
			},
			wantNotMsgs: []string{"set prefix:"},
		},
		{
			name: "unrelated path keeps prefix half",
			url:  "https://mimir.example/api/v1/foo",
			wantMsgs: []string{
				"set prefix: /alertmanager",
				"tenant_header: X-Scope-OrgID",
			},
		},
		{
			name: "garbage URL falls through, prints both halves",
			url:  "://broken",
			wantMsgs: []string{
				"set prefix: /alertmanager",
				"tenant_header: X-Scope-OrgID",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mimirSetupHint(tc.url)
			for _, want := range tc.wantMsgs {
				require.Contains(t, got, want)
			}
			for _, notWant := range tc.wantNotMsgs {
				require.NotContains(t, got, notWant)
			}
		})
	}
}

// TestRunInit_PrintsMimirHint pins the end-to-end wiring: a wizard
// run lands the Mimir setup hint on stderr, alongside the existing
// plaintext-credential hint, so stdout-piping CI captures stay
// pipe-clean while the operator's terminal still sees the footer.
func TestRunInit_PrintsMimirHint(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	in := initInputs("none", "", "")
	err := runInit(initIO{
		In:    strings.NewReader(in),
		Out:   &out,
		Err:   &errBuf,
		Flags: &GlobalFlags{ConfigDir: dir},
	})
	require.NoError(t, err)
	require.Contains(t, out.String(), "wrote ")
	require.Contains(t, errBuf.String(), "set prefix: /alertmanager",
		"Mimir setup footer must land on stderr after the wizard succeeds")
	require.Contains(t, errBuf.String(), "tenant_header: X-Scope-OrgID")
}

// minimal --kv set every one-shot run needs. Helpers compose on top
// via append(minKVs(), ...) so a test that exercises one knob
// doesn't repeat the boilerplate. Per ADR 0039 there is no `kind`
// key.
func minKVs() []string {
	return []string{
		"name=prod",
		"url=https://am.example",
	}
}

func TestParseKVAnswers(t *testing.T) {
	t.Parallel()

	// Cases differ in what they assert about the parsed answers (one
	// field vs. many vs. an error message), so each row owns a check
	// closure. Sibling tests below (Malformed, BuildInitConfig
	// rejections) keep their own dedicated tables — they exercise
	// distinct invariants.
	cases := []struct {
		name  string
		kvs   []string
		check func(t *testing.T, ans initAnswers, err error)
	}{
		{
			name: "minimal",
			kvs:  minKVs(),
			check: func(t *testing.T, ans initAnswers, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, "prod", ans.Name)
				require.Equal(t, "https://am.example", ans.URL)
				require.False(t, ans.PrefixSet)
				require.False(t, ans.TenantSet)
				require.Empty(t, ans.AuthMode)
			},
		},
		{
			// Repeated --kv name=foo --kv name=bar must surface the
			// second value: cobra's StringArrayVar preserves order, and
			// the operator reading their own command line top-to-bottom
			// expects the last override to win.
			name: "last write wins",
			kvs: []string{
				"name=first", "name=second",
				"url=https://am.example",
			},
			check: func(t *testing.T, ans initAnswers, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Equal(t, "second", ans.Name)
			},
		},
		{
			// `tenant=` is legal — single-tenant Mimir leaves the slot
			// blank — and must round-trip as an empty string with
			// TenantSet flipped so buildInitConfig can distinguish it
			// from "user didn't pass tenant at all".
			name: "empty value retained",
			kvs: []string{
				"name=mimir", "url=https://mimir.example",
				"tenant=",
			},
			check: func(t *testing.T, ans initAnswers, err error) {
				t.Helper()
				require.NoError(t, err)
				require.Empty(t, ans.Tenant)
				require.True(t, ans.TenantSet,
					"explicit empty value must still flip the *Set marker")
			},
		},
		{
			// Per ADR 0039 `kind` is no longer a recognised key.
			// Operators on older scripts get a loud "unknown key"
			// error rather than silently-honoured no-op.
			name: "kind is no longer recognised",
			kvs:  append(minKVs(), "kind=mimir"),
			check: func(t *testing.T, _ initAnswers, err error) {
				t.Helper()
				require.Error(t, err)
				require.Contains(t, err.Error(), `unknown key "kind"`)
			},
		},
		{
			name: "unknown key fails closed",
			kvs:  append(minKVs(), "foo=bar"),
			check: func(t *testing.T, _ initAnswers, err error) {
				t.Helper()
				require.Error(t, err)
				require.Contains(t, err.Error(), `unknown key "foo"`)
				require.Contains(t, err.Error(), "recognised keys are")
				// Echoes a few of the known keys so the operator gets
				// a pointer toward what they should have typed.
				require.Contains(t, err.Error(), "name")
				require.Contains(t, err.Error(), "url")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ans, err := parseKVAnswers(tc.kvs)
			tc.check(t, ans, err)
		})
	}
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

func TestBuildInitConfig_OneShotMatchesWizard(t *testing.T) {
	t.Parallel()

	// Same explicit inputs through both flows must produce the
	// same Config, byte-for-byte after YAML encoding. Locks the
	// "one helper, two paths" guarantee.
	wizardIn := initInputs("none", "", "")
	wizardCfg, err := promptConfig(strings.NewReader(wizardIn), &bytes.Buffer{})
	require.NoError(t, err)

	ans, err := parseKVAnswers([]string{
		"name=prod",
		"url=https://am.example",
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

func TestBuildInitConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		kvs           []string
		wantErrSubstr string                                // non-empty → error case
		check         func(t *testing.T, cfg config.Config) // non-nil → success-case extra assertions
	}{
		{name: "missing all", kvs: nil, wantErrSubstr: "name, url"},
		{name: "missing name", kvs: []string{"url=https://x"}, wantErrSubstr: "name"},
		{name: "missing url", kvs: []string{"name=p"}, wantErrSubstr: "url"},
		{
			// Explicit prefix passes straight through to the Backend
			// — no wizard-time gate. The bare-Mimir operator scripting
			// init reaches for this knob directly.
			name: "explicit prefix sets backend.Prefix",
			kvs:  append(minKVs(), "prefix=/alertmanager"),
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				require.Equal(t, "/alertmanager", cfg.Backends[0].Prefix)
			},
		},
		{
			name: "explicit tenant sets X-Scope-OrgID header",
			kvs:  append(minKVs(), "tenant=tenant-a"),
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				require.Equal(t, "X-Scope-OrgID", cfg.Backends[0].TenantHeader)
				require.Equal(t, "tenant-a", cfg.Backends[0].Tenant)
			},
		},
		{
			// Explicit empty tenant is the single-tenant Mimir case — the
			// header must NOT be injected, otherwise Mimir would 400 on
			// every request.
			name: "empty tenant omits header",
			kvs:  append(minKVs(), "tenant="),
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				require.Empty(t, cfg.Backends[0].TenantHeader)
				require.Empty(t, cfg.Backends[0].Tenant)
			},
		},
		{
			name:          "bearer requires token",
			kvs:           append(minKVs(), "auth_mode=bearer"),
			wantErrSubstr: "auth_mode=bearer requires bearer_token",
		},
		// user-only and password-only together pin "both required" — a
		// regression to "only user required" fails basic-missing-password,
		// a regression to "only password required" fails basic-missing-user.
		// The both-empty case adds no catching power (every regression that
		// breaks one leg keeps both-empty erroring) so it stays cut.
		{
			name:          "basic missing password",
			kvs:           append(minKVs(), "auth_mode=basic", "basic_user=alice"),
			wantErrSubstr: "auth_mode=basic requires both basic_user and basic_password",
		},
		{
			name:          "basic missing user",
			kvs:           append(minKVs(), "auth_mode=basic", "basic_password=hunter2"),
			wantErrSubstr: "auth_mode=basic requires both basic_user and basic_password",
		},
		{
			// Default (unset) auth_mode is none. Setting bearer_token then
			// must surface the contradiction rather than silently dropping
			// the token on the floor.
			name:          "none rejects credentials",
			kvs:           append(minKVs(), "bearer_token=secret"),
			wantErrSubstr: "auth_mode=none forbids",
		},
		{
			name:          "bad auth mode",
			kvs:           append(minKVs(), "auth_mode=oauth"),
			wantErrSubstr: `auth_mode "oauth"`,
		},
		{
			name:          "bad theme",
			kvs:           append(minKVs(), "theme=solarized"),
			wantErrSubstr: `theme "solarized"`,
		},
		{
			name:          "bad poll interval",
			kvs:           append(minKVs(), "poll_interval=forever"),
			wantErrSubstr: "duration",
		},
		{
			// poll_interval omitted → 30s default (matches wizard prompt
			// default). Prevents a one-shot run from emitting a Config with
			// PollInterval zero, which would resolve to the package floor
			// rather than the user's expected 30s.
			name: "poll default when omitted",
			kvs:  minKVs(),
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				require.Equal(t, 30*time.Second, cfg.Defaults.PollInterval)
				require.Equal(t, "catppuccin-mocha", cfg.Theme.Name)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ans, err := parseKVAnswers(tc.kvs)
			require.NoError(t, err)
			cfg, err := buildInitConfig(ans)
			if tc.wantErrSubstr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErrSubstr)
				return
			}
			require.NoError(t, err)
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
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
		KVs:     []string{"name=prod"}, // missing url
	})
	require.Error(t, err)
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitConfigInvalid, ee.Code)
}

// TestRunInit_NudgesOnPlaintextBasicPassword pins the init-wizard
// nudge: when the captured config carries a literal basic-auth
// password (not a `${VAR}` interpolation), init prints a one-line
// nudge after the "wrote" confirmation pointing the operator at
// env-var interpolation. The hint goes to stderr so scripts piping
// stdout (`a10r init | tee ...`) still see clean confirmation output.
func TestRunInit_NudgesOnPlaintextBasicPassword(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var errBuf bytes.Buffer
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &errBuf,
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		KVs: []string{
			"name=prod",
			"url=https://am.example",
			"auth_mode=basic",
			"basic_user=alice",
			"basic_password=hunter2",
		},
	})
	require.NoError(t, err)

	hint := errBuf.String()
	require.Contains(t, hint, "${A10R_BACKEND_PROD_PASSWORD}",
		"hint must reference the backend name in upper-case so the export line is copy-paste ready")
	require.Contains(t, hint, "plaintext")
}

// TestRunInit_NudgesOnPlaintextBearerToken extends the nudge to the
// bearer flow — bearer tokens are credentials too.
func TestRunInit_NudgesOnPlaintextBearerToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var errBuf bytes.Buffer
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &errBuf,
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		KVs: []string{
			"name=prod",
			"url=https://am.example",
			"auth_mode=bearer",
			"bearer_token=supersecret",
		},
	})
	require.NoError(t, err)
	require.Contains(t, errBuf.String(), "${A10R_BACKEND_PROD_TOKEN}",
		"bearer flow must suggest a _TOKEN suffix — _PASSWORD would mislead a copy-pasting operator")
	require.Contains(t, errBuf.String(), "plaintext")
}

// TestRunInit_NoPlaintextNudgeOnInterpolatedCredential covers the
// silent path: if the operator already passed `${VAR}` for the
// credential value, they've solved the problem the nudge is trying
// to point at. The Mimir setup hint still fires unconditionally
// (different concern), but the plaintext-credential channel goes
// quiet.
func TestRunInit_NoPlaintextNudgeOnInterpolatedCredential(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var errBuf bytes.Buffer
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &errBuf,
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		KVs: []string{
			"name=prod",
			"url=https://am.example",
			"auth_mode=basic",
			"basic_user=alice",
			"basic_password=${MY_PASSWORD}",
		},
	})
	require.NoError(t, err)
	require.NotContains(t, errBuf.String(), "plaintext",
		"credential already an interpolation -> no plaintext nudge")
}

// TestRunInit_DryRunSuppressesPlaintextNudge keeps the preview path
// quiet for the plaintext-credential channel: `--dry-run` does not
// actually persist credentials anywhere, so the warning is advice
// without a target. The Mimir setup footer also stays silent in
// dry-run because the write code path is the one that prints it.
func TestRunInit_DryRunSuppressesPlaintextNudge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var errBuf bytes.Buffer
	err := runInit(initIO{
		In:      strings.NewReader(""),
		Out:     &bytes.Buffer{},
		Err:     &errBuf,
		Flags:   &GlobalFlags{ConfigDir: dir},
		OneShot: true,
		DryRun:  true,
		KVs: []string{
			"name=prod",
			"url=https://am.example",
			"auth_mode=basic",
			"basic_user=alice",
			"basic_password=hunter2",
		},
	})
	require.NoError(t, err)
	require.NotContains(t, errBuf.String(), "plaintext",
		"--dry-run writes nothing -> no plaintext nudge")
}

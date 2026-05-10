// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestPromptConfig_MimirEmptyTenantOmitsHeader(t *testing.T) {
	t.Parallel()

	// Single-tenant Mimir (auth.multitenancy disabled) leaves the
	// tenant ID blank. The generated config must NOT carry
	// `tenant_header: X-Scope-OrgID` with no value — Mimir rejects
	// the header injection without a payload, and the file would
	// not round-trip through config.Load's
	// tenant-header/tenant-collision validation either.
	in := initInputs(
		"mimir-single", "https://mimir.example", "mimir", "",
		"none", "", "",
		"60s", "catppuccin-mocha",
	)
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Equal(t, "/alertmanager", cfg.Backends[0].Prefix,
		"prefix is unconditional — Mimir's AM lives under /alertmanager")
	require.Empty(t, cfg.Backends[0].TenantHeader,
		"empty tenant input must leave tenant_header unset")
	require.Empty(t, cfg.Backends[0].Tenant)
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

func TestPromptConfig_MimirURLWithPrefixSkipsAdd(t *testing.T) {
	t.Parallel()

	// User provided URL that already encodes /alertmanager; the
	// wizard's default prefix becomes empty so the resulting
	// Backend has Prefix="" — without this fix a request would
	// hit https://mimir.example/alertmanager/alertmanager/api/v2/...
	in := strings.Join([]string{
		"primary",
		"https://mimir.example/alertmanager",
		"mimir",
		"", // prefix prompt — accept the empty default
		"", // tenant — blank
		"none",
		"30s",
		"catppuccin-mocha",
	}, "\n") + "\n"
	cfg, err := promptConfig(strings.NewReader(in), &bytes.Buffer{})
	require.NoError(t, err)
	require.Empty(t, cfg.Backends[0].Prefix,
		"URL already carrying /alertmanager must not get the prefix doubled")
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

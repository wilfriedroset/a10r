// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/uistate"
	"github.com/wilfriedroset/a10r/internal/xdg"
)

// Run `go test ./cmd -update -run TestRenderInfo` to regenerate
// every cmd/testdata/*.golden when the renderer's expected output
// changes. Without the flag, assertGolden reads and compares.
var updateGolden = flag.Bool("update", false, "regenerate golden files under testdata/")

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(body), got)
}

func TestRenderInfo_FullConfig(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Backends: []config.Backend{
			{
				Name:        "prod-vanilla",
				URL:         "https://am-prod.internal",
				BearerToken: "tok",
			},
			{
				Name:         "staging-mimir",
				URL:          "https://mimir-staging.internal",
				Prefix:       "/alertmanager",
				TenantHeader: "X-Scope-OrgID",
				Tenant:       "tenant-a",
				Capabilities: config.Capabilities{ConfigAPI: true, TenantAdmin: true},
				BasicAuth:    &config.BasicAuth{Username: "u", Password: "p"},
			},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		Version:   "dev",
		Commit:    "test",
		Date:      "test",
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		StateDir:  "/home/test/.local/state/a10r",
		Config:    cfg,
		// An explicit theme is the branch where the label names the
		// skin; the other goldens cover the auto default.
		Theme: "catppuccin-latte",
	}))
	assertGolden(t, "info_full.golden", buf.String())
}

func TestRenderInfo_EmptyBackendsList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		Version:   "dev",
		Commit:    "test",
		Date:      "test",
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		StateDir:  "/home/test/.local/state/a10r",
		Config:    &config.Config{},
		Theme:     config.DefaultThemeName,
	}))
	assertGolden(t, "info_empty.golden", buf.String())
}

func TestRenderInfo_NotFound(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		Version:   "dev",
		Commit:    "test",
		Date:      "test",
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		StateDir:  "/home/test/.local/state/a10r",
		NotFound:  true,
		Theme:     config.DefaultThemeName,
	}))
	assertGolden(t, "info_notfound.golden", buf.String())
}

func TestRenderInfo_NonZeroAliases(t *testing.T) {
	t.Parallel()

	// The alias count is the operator's signal that
	// <config-dir>/aliases.yaml landed where they expected — pin
	// the rendered line so a regression in formatting is loud.
	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		Version:    "dev",
		Commit:     "test",
		Date:       "test",
		ConfigDir:  "/home/test/.config/a10r",
		LogPath:    "/home/test/.local/state/a10r/a10r.log",
		StateDir:   "/home/test/.local/state/a10r",
		Config:     &config.Config{},
		AliasCount: 3,
		Theme:      config.DefaultThemeName,
	}))
	assertGolden(t, "info_aliases.golden", buf.String())
}

func TestRenderInfo_RememberedScope(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		Version:         "dev",
		Commit:          "test",
		Date:            "test",
		ConfigDir:       "/home/test/.config/a10r",
		LogPath:         "/home/test/.local/state/a10r/a10r.log",
		StateDir:        "/home/test/.local/state/a10r",
		Config:          &config.Config{},
		Theme:           config.DefaultThemeName,
		RememberedScope: "prod,staging",
	}))
	assertGolden(t, "info_remember.golden", buf.String())
}

// info is a diagnostic, so opening the state store must leave the
// file byte-for-byte alone. A later flush-on-Close or normalize-on-
// Open inside internal/uistate would otherwise make `a10r info`
// rewrite a hand-edited file with nothing failing.
func TestRunInfo_LeavesTheStateFileAlone(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv(xdg.StateHome, stateHome)

	stateDir := filepath.Join(stateHome, "a10r")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	statePath := filepath.Join(stateDir, uistate.FileName)
	// Deliberately not what yaml.Marshal emits: a comment and a
	// 2-space indent both parse fine and both die in a rewrite, so
	// the byte comparison below fails on a flush info must never do.
	body := "# hand-edited\nscope: prod\nsort:\n  alerts: severity:asc\n"
	require.NoError(t, os.WriteFile(statePath, []byte(body), 0o600))

	cfgDir := t.TempDir()
	writeYAML(t, cfgDir, "a10r.yaml",
		"backends:\n  - name: prod\n    url: http://x\ntui:\n  remember: true\n")

	var buf bytes.Buffer
	require.NoError(t, runInfo(&buf, &GlobalFlags{
		ConfigDir: cfgDir,
		LogPath:   filepath.Join(stateDir, "a10r.log"),
	}))
	require.Contains(t, buf.String(), "scope:      prod (remembered)")

	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	require.Equal(t, body, string(after))

	tmps, err := filepath.Glob(filepath.Join(stateDir, "*.tmp"))
	require.NoError(t, err)
	require.Empty(t, tmps)
}

// A remembered tenant the config dropped must not reach the report:
// info answers "why did a10r open there", and boot prunes the same
// name away before it ever opens.
func TestRunInfo_PrunesTheRememberedScope(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv(xdg.StateHome, stateHome)

	stateDir := filepath.Join(stateHome, "a10r")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(stateDir, uistate.FileName), []byte("scope: gone\n"), 0o600,
	))

	cfgDir := t.TempDir()
	writeYAML(t, cfgDir, "a10r.yaml",
		"backends:\n  - name: prod\n    url: http://x\ntui:\n  remember: true\n")

	var buf bytes.Buffer
	require.NoError(t, runInfo(&buf, &GlobalFlags{
		ConfigDir: cfgDir,
		LogPath:   filepath.Join(stateDir, "a10r.log"),
	}))
	require.NotContains(t, buf.String(), "gone")
	require.NotContains(t, buf.String(), "(remembered)")
}

// An unresolvable state dir must drop the line rather than print an
// empty one, because info runs on hosts where HOME is unset.
func TestRenderInfo_NoStateDirDropsTheLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		Config:    &config.Config{},
		Theme:     config.DefaultThemeName,
	}))
	require.NotContains(t, buf.String(), "state dir:")
}

func TestAuthLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   config.Backend
		want string
	}{
		{name: "no auth yields empty"},
		{
			name: "basic",
			in:   config.Backend{BasicAuth: &config.BasicAuth{Username: "u", Password: "p"}},
			want: "basic",
		},
		{
			name: "bearer_token shorthand",
			in:   config.Backend{BearerToken: "tok"},
			want: "bearer",
		},
		{
			name: "authorization echoes the wire scheme",
			in: config.Backend{
				Authorization: &config.Authorization{Type: "Bearer", Credentials: "tok"},
			},
			want: "authorization (Bearer)",
		},
		{
			name: "authorization with custom type",
			in: config.Backend{
				Authorization: &config.Authorization{Type: "Token", Credentials: "tok"},
			},
			want: "authorization (Token)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, authLabel(tc.in))
		})
	}
}

func TestCapabilityList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   config.Capabilities
		want string
	}{
		{name: "all off yields empty"},
		{name: "config_api only", in: config.Capabilities{ConfigAPI: true}, want: "config_api"},
		{
			name: "all on, declaration order preserved",
			in:   config.Capabilities{ConfigAPI: true, TenantAdmin: true, Ring: true},
			want: "config_api, tenant_admin, ring",
		},
		{
			name: "skip middle off",
			in:   config.Capabilities{ConfigAPI: true, Ring: true},
			want: "config_api, ring",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, capabilityList(tc.in))
		})
	}
}

func TestThemeLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "unset reads as the auto default", in: "", want: "auto (terminal decides at start)"},
		{
			name: "cli auto over a named file value", in: config.ResolveTheme("auto", "catppuccin-latte"),
			want: "auto (terminal decides at start)",
		},
		{name: "explicit auto", in: "auto", want: "auto (terminal decides at start)"},
		{name: "named skin", in: "catppuccin-latte", want: "catppuccin-latte"},
		{name: "user skin", in: "gruvbox-dark", want: "gruvbox-dark"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, themeLabel(tc.in))
		})
	}
}

func TestRenderInfo_Guardrails(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Backends: []config.Backend{{Name: "prod-eu", URL: "https://am-prod-eu.internal"}},
		Guardrails: guardrail.Set{
			{
				Tenants: []string{"prod-*"},
				Actions: []string{"silence.expire"},
				Deny:    true,
				Reason:  "change ticket only",
			},
			{Tenants: []string{"prod-*"}, Confirmation: guardrail.ConfirmationTypeTenantName},
			{MaxBulk: 20},
			// A glob no backend answers: the shared config.d fragment
			// case the report warns about rather than rejecting.
			{Tenants: []string{"lab-*"}, Deny: true},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, renderInfo(&buf, infoContext{
		Version:   "dev",
		Commit:    "test",
		Date:      "test",
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		StateDir:  "/home/test/.local/state/a10r",
		Config:    cfg,
		Theme:     config.DefaultThemeName,
	}))
	assertGolden(t, "info_guardrails.golden", buf.String())
}

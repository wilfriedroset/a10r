// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
)

// Run `go test ./internal/report -update` to regenerate every
// testdata/*.golden when a renderer's expected output changes.
// Without the flag, assertGolden reads and compares.
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

func TestInfo_FullConfig(t *testing.T) {
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
			{
				// Inline userinfo is the shape the redaction guards
				// against, so the golden proves the strip rather than
				// merely surviving it.
				Name: "legacy-inline",
				URL:  "https://user:pass@am-legacy.internal",
			},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
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

func TestInfo_EmptyBackendsList(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
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

func TestInfo_NotFound(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
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

func TestInfo_NonZeroAliases(t *testing.T) {
	t.Parallel()

	// The alias count is the operator's signal that
	// <config-dir>/aliases.yaml landed where they expected — pin
	// the rendered line so a regression in formatting is loud.
	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
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

func TestInfo_RememberedScope(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
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

// An unresolvable state dir must drop the line rather than print an
// empty one, because info runs on hosts where HOME is unset.
func TestInfo_NoStateDirDropsTheLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
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
		{
			name: "credentials in the url",
			in:   config.Backend{URL: "https://__PK_BASICAUTH_1a15d1cf67f9__@am.internal"},
			want: "url userinfo",
		},
		{
			name: "a configured auth field beats the url",
			in: config.Backend{
				URL:         "https://__PK_BASICAUTH_1a15d1cf67f9__@am.internal",
				BearerToken: "tok",
			},
			want: "bearer",
		},
		{
			name: "a url without userinfo yields empty",
			in:   config.Backend{URL: "https://am.internal"},
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

func TestInfo_Guardrails(t *testing.T) {
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
	require.NoError(t, Info(&buf, InfoInput{
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

func TestInfo_Notify(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Backends: []config.Backend{{Name: "prod-eu", URL: "https://am-prod-eu.internal"}},
		// A guardrail rule rides along so the golden pins the block
		// order: notify comes after guardrails.
		Guardrails: guardrail.Set{{Tenants: []string{"prod-*"}, Deny: true}},
		TUI: config.TUI{Notify: config.Notify{
			Enabled:     true,
			Bell:        new(false),
			Desktop:     config.NotifyDesktopBoth,
			MinSeverity: "critical",
			Command:     []string{"notify-send", "a10r", config.NotifyMessagePlaceholder},
		}},
	}

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
		Version:   "dev",
		Commit:    "test",
		Date:      "test",
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		StateDir:  "/home/test/.local/state/a10r",
		Config:    cfg,
		Theme:     config.DefaultThemeName,
	}))
	assertGolden(t, "info_notify.golden", buf.String())
}

func TestInfo_NotifyDisabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		notify config.Notify
	}{
		{name: "unset"},
		{
			name:   "configured but off",
			notify: config.Notify{Desktop: config.NotifyDesktopBoth, Command: []string{"notify-send", "a10r"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			require.NoError(t, Info(&buf, InfoInput{
				Version:   "dev",
				Commit:    "test",
				Date:      "test",
				ConfigDir: "/home/test/.config/a10r",
				LogPath:   "/home/test/.local/state/a10r/a10r.log",
				Config:    &config.Config{TUI: config.TUI{Notify: tc.notify}},
				Theme:     config.DefaultThemeName,
			}))
			require.NotContains(t, buf.String(), "notify:")
		})
	}
}

func TestInfo_NotifyDefaults(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, Info(&buf, InfoInput{
		Version:   "dev",
		Commit:    "test",
		Date:      "test",
		ConfigDir: "/home/test/.config/a10r",
		LogPath:   "/home/test/.local/state/a10r/a10r.log",
		Config:    &config.Config{TUI: config.TUI{Notify: config.Notify{Enabled: true}}},
		Theme:     config.DefaultThemeName,
	}))
	require.Contains(t, buf.String(), "\nnotify:\n  desktop:      osc777\n  min_severity: warning\n  bell:         on\n")
	require.NotContains(t, buf.String(), "command:")
}

func TestInfo_NotifyCommandArgCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		argv []string
		want string
	}{
		{name: "program alone", argv: []string{"notify-send"}, want: "  command:      notify-send\n"},
		{name: "one argument", argv: []string{"ntfy", "publish"}, want: "  command:      ntfy (+1 arg)\n"},
		{name: "several arguments", argv: []string{"ntfy", "publish", "--token", "secret"}, want: "  command:      ntfy (+3 args)\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			require.NoError(t, Info(&buf, InfoInput{
				Version:   "dev",
				Commit:    "test",
				Date:      "test",
				ConfigDir: "/home/test/.config/a10r",
				LogPath:   "/home/test/.local/state/a10r/a10r.log",
				Config: &config.Config{TUI: config.TUI{Notify: config.Notify{
					Enabled: true,
					Command: tc.argv,
				}}},
				Theme: config.DefaultThemeName,
			}))
			require.Contains(t, buf.String(), tc.want)
			require.NotContains(t, buf.String(), "secret")
		})
	}
}

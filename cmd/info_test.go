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
		Config:    cfg,
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
		Config:    &config.Config{},
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
		NotFound:  true,
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
		Config:     &config.Config{},
		AliasCount: 3,
	}))
	assertGolden(t, "info_aliases.golden", buf.String())
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

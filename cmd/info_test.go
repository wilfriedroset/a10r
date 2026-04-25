// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
)

func readGolden(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(body)
}

func TestRenderInfo_FullConfig(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Backends: []config.Backend{
			{
				Name: "prod-vanilla",
				URL:  "https://am-prod.internal",
				Auth: &config.AuthSpec{Type: config.AuthTypeBearer},
			},
			{
				Name:         "staging-mimir",
				URL:          "https://mimir-staging.internal",
				Prefix:       "/alertmanager",
				TenantHeader: "X-Scope-OrgID",
				Tenant:       "tenant-a",
				Capabilities: config.Capabilities{ConfigAPI: true, TenantAdmin: true},
				Auth:         &config.AuthSpec{Type: config.AuthTypeBasic},
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
	require.Equal(t, readGolden(t, "info_full.golden"), buf.String())
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
	require.Equal(t, readGolden(t, "info_empty.golden"), buf.String())
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
	require.Equal(t, readGolden(t, "info_notfound.golden"), buf.String())
}

func TestAuthTypeLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   *config.AuthSpec
		want string
	}{
		{name: "nil spec yields empty", want: ""},
		{name: "empty type yields empty", in: &config.AuthSpec{}, want: ""},
		{name: "basic", in: &config.AuthSpec{Type: config.AuthTypeBasic}, want: "basic"},
		{name: "bearer", in: &config.AuthSpec{Type: config.AuthTypeBearer}, want: "bearer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, authTypeLabel(tc.in))
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

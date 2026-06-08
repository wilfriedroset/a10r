// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// threeBackendConfig writes a config fixture with prod/staging/dev and
// returns its path. The URLs are unreachable on purpose: loadCmdConfig
// only parses and scopes, it never dials.
func threeBackendConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
backends:
  - name: prod
    url: http://127.0.0.1:1
  - name: staging
    url: http://127.0.0.1:2
  - name: dev
    url: http://127.0.0.1:3
`), 0o600))
	return cfgPath
}

func backendNames(t *testing.T, cfgPath, tenant string) []string {
	t.Helper()
	cfg, err := loadCmdConfig(&GlobalFlags{ConfigPath: cfgPath, Tenant: tenant})
	require.NoError(t, err)
	names := make([]string, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		names = append(names, b.Name)
	}
	return names
}

func TestLoadCmdConfig_ScopesByTenant(t *testing.T) {
	t.Parallel()
	cfgPath := threeBackendConfig(t)

	require.Equal(t, []string{"prod", "staging", "dev"}, backendNames(t, cfgPath, ""))
	require.Equal(t, []string{"prod", "staging", "dev"}, backendNames(t, cfgPath, "all"))
	require.Equal(t, []string{"staging"}, backendNames(t, cfgPath, "staging"))
	require.Equal(t, []string{"prod", "dev"}, backendNames(t, cfgPath, "prod,dev"))
}

func TestLoadCmdConfig_UnknownTenantErrors(t *testing.T) {
	t.Parallel()
	cfgPath := threeBackendConfig(t)

	_, err := loadCmdConfig(&GlobalFlags{ConfigPath: cfgPath, Tenant: "nope"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nope")

	// A list with one valid and one typo'd element errors on the typo
	// rather than silently narrowing to the valid subset.
	_, err = loadCmdConfig(&GlobalFlags{ConfigPath: cfgPath, Tenant: "prod,bogus"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
	require.NotContains(t, err.Error(), `"prod"`, "the valid element is not reported as unknown")
}

func TestLoadWriteConfig_ReadOnlyAndScope(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
backends:
  - name: prod
    url: http://127.0.0.1:1
  - name: staging
    url: http://127.0.0.1:2
    read_only: true
`), 0o600))

	// Per-backend read_only survives the load, and the global knob is
	// false by default.
	cfg, globalRO, err := loadWriteConfig(&GlobalFlags{ConfigPath: cfgPath})
	require.NoError(t, err)
	require.False(t, globalRO)
	require.Len(t, cfg.Backends, 2)
	require.True(t, cfg.Backends[1].ReadOnly, "per-backend read_only preserved")

	// --read-only flag forces the global knob true.
	_, globalRO, err = loadWriteConfig(&GlobalFlags{ConfigPath: cfgPath, ReadOnly: true})
	require.NoError(t, err)
	require.True(t, globalRO)

	// --tenant narrows the write target set like the read path.
	cfg, _, err = loadWriteConfig(&GlobalFlags{ConfigPath: cfgPath, Tenant: "prod"})
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "prod", cfg.Backends[0].Name)
}

// TestLoadWriteConfig_EmptyConfigErrors locks that a write verb refuses
// to run against a config with no backends: there is nothing to write
// to, so it must fail with a clear message rather than the silent
// exit-0 no-op a zero-target fan-out would otherwise produce.
func TestLoadWriteConfig_EmptyConfigErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("backends: []\n"), 0o600))

	for _, tenant := range []string{"", "prod"} {
		_, _, err := loadWriteConfig(&GlobalFlags{ConfigPath: cfgPath, Tenant: tenant})
		require.Error(t, err)
		// Even with a tenant filter, an empty config reports the root
		// cause (no backends) rather than "no backend matches --tenant".
		require.Contains(t, err.Error(), "no backends configured", "tenant=%q", tenant)
		var ex *ExitError
		require.ErrorAs(t, err, &ex)
		require.Equal(t, ExitConfigInvalid, ex.Code)
	}
}

// TestLoadCmdConfig_EmptyConfigNoError locks the guard that a
// zero-backend config (loadable: Validate does not require a backend)
// with no --tenant must NOT trip the "no backend matches" error — the
// empty-scope path is distinct from a typo'd tenant.
func TestLoadCmdConfig_EmptyConfigNoError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("backends: []\n"), 0o600))

	cfg, err := loadCmdConfig(&GlobalFlags{ConfigPath: cfgPath})
	require.NoError(t, err)
	require.Empty(t, cfg.Backends)
}

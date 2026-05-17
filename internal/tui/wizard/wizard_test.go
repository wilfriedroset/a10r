// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/config"
)

func TestBuild_RequiresURL(t *testing.T) {
	t.Parallel()
	_, err := Build(Input{Name: "prod"})
	require.ErrorContains(t, err, "URL is required")
}

func TestBuild_RequiresName(t *testing.T) {
	t.Parallel()
	_, err := Build(Input{URL: "https://am"})
	require.ErrorContains(t, err, "name is required")
}

func TestBuild_NoAuth(t *testing.T) {
	t.Parallel()
	cfg, err := Build(Input{
		Name: "prod",
		URL:  "https://am.example",
	})
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "https://am.example", cfg.Backends[0].URL)
	require.Nil(t, cfg.Backends[0].BasicAuth)
	require.Nil(t, cfg.Backends[0].Authorization)
	require.Empty(t, cfg.Backends[0].BearerToken)
	require.Empty(t, cfg.Backends[0].Headers)
}

func TestBuild_BasicAuth(t *testing.T) {
	t.Parallel()
	cfg, err := Build(Input{
		Name: "prod", URL: "https://am",
		AuthType: AuthBasic, BasicUser: "alice", BasicPass: "${PW}",
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.Backends[0].BasicAuth)
	require.Equal(t, "alice", cfg.Backends[0].BasicAuth.Username)
	require.Equal(t, "${PW}", cfg.Backends[0].BasicAuth.Password)
}

func TestBuild_BasicAuthMissingFieldErrors(t *testing.T) {
	t.Parallel()
	_, err := Build(Input{
		Name: "prod", URL: "https://am",
		AuthType: AuthBasic, BasicUser: "alice",
	})
	require.ErrorContains(t, err, "username and password")
}

func TestBuild_BearerAuth(t *testing.T) {
	t.Parallel()
	cfg, err := Build(Input{
		Name: "prod", URL: "https://am",
		AuthType: AuthBearer, BearerToken: "tok",
	})
	require.NoError(t, err)
	require.Equal(t, "tok", cfg.Backends[0].BearerToken)
}

func TestBuild_HeaderAuth(t *testing.T) {
	t.Parallel()
	cfg, err := Build(Input{
		Name: "prod", URL: "https://am",
		AuthType: AuthHeader, HeaderName: "X-Auth", HeaderValue: "v",
	})
	require.NoError(t, err)
	require.Equal(t, "v", cfg.Backends[0].Headers["X-Auth"],
		"AuthHeader sugar must materialise as a single Headers entry")
}

func TestBuild_TenantFields(t *testing.T) {
	t.Parallel()
	cfg, err := Build(Input{
		Name:         "mimir-prod",
		URL:          "https://mimir.example",
		Prefix:       "/api/prom",
		TenantHeader: "X-Scope-OrgID",
		TenantValue:  "tenant-1",
	})
	require.NoError(t, err)
	be := cfg.Backends[0]
	require.Equal(t, "/api/prom", be.Prefix)
	require.Equal(t, "X-Scope-OrgID", be.TenantHeader)
	require.Equal(t, "tenant-1", be.Tenant)
}

func TestWrite_RoundTripsThroughYaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := Build(Input{Name: "prod", URL: "https://am.example"})
	require.NoError(t, err)
	path, err := Write(dir, cfg)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "a10r.yaml"), path)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "# a10r")

	var got config.Config
	require.NoError(t, yaml.Unmarshal(body, &got))
	require.Len(t, got.Backends, 1)
	require.Equal(t, "https://am.example", got.Backends[0].URL)
}

func TestWrite_RefusesOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := Build(Input{Name: "prod", URL: "https://am"})
	require.NoError(t, err)
	_, err = Write(dir, cfg)
	require.NoError(t, err)
	_, err = Write(dir, cfg)
	require.Error(t, err, "second Write must refuse to overwrite")
}

func TestRun_ValidationErrorSurfaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := Run(dir, Input{}, nil)
	require.ErrorContains(t, err, "URL is required")
	// Tempdir is empty — the failed Build must NOT have written anything.
	entries, _ := os.ReadDir(dir)
	require.Empty(t, entries)
}

// TestRun_PrintsExportHintAfterBasicAuth pins audit F5: the
// wizard captured plaintext credentials, so it nudges the user
// toward ${VAR} interpolation as a follow-up. The hint references
// the backend name verbatim so the operator can copy-paste the
// export line.
func TestRun_PrintsExportHintAfterBasicAuth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var buf strings.Builder
	in := Input{
		Name:      "prod",
		URL:       "https://am",
		AuthType:  AuthBasic,
		BasicUser: "alice",
		BasicPass: "hunter2",
	}
	_, err := Run(dir, in, &buf)
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "${A10R_BACKEND_PROD_PASSWORD}",
		"hint must reference the backend name in upper-case so the export line is copy-paste ready")
	require.Contains(t, out, "plaintext")
}

// TestRun_NoHintForAuthlessConfig keeps the noise floor low: no
// auth means no plaintext to nudge about.
func TestRun_NoHintForAuthlessConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var buf strings.Builder
	_, err := Run(dir, Input{Name: "open", URL: "https://am"}, &buf)
	require.NoError(t, err)
	require.Empty(t, buf.String(),
		"no plaintext credential captured -> no hint printed")
}

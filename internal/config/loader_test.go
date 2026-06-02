// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stagingFixture mirrors the inline contents of valid_full.yaml so
// loader tests can write a fresh copy into a t.TempDir without
// reaching for testdata/. Keeping the fixture source-of-truth in
// types_test.go for the unmarshal-only tests and inline here for
// the loader tests avoids circular setup ordering.
const stagingFixture = `defaults:
  poll_interval: 30s
  read_only: false
  log_format: json

theme:
  name: gruvbox-dark

backends:
  - name: prod-vanilla
    url: https://am-prod.internal
    bearer_token: ${A10R_PROD_TOKEN}
`

func writeFixtureToDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, defaultConfigFile), []byte(body), 0o600))
	return dir
}

func TestLoad_ValidWithEnvInterpolation(t *testing.T) {
	t.Parallel()

	dir := writeFixtureToDir(t, stagingFixture)
	env := func(k string) string {
		if k == "A10R_PROD_TOKEN" {
			return "secret-prod"
		}
		return ""
	}

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, env, func() (string, error) { return "/u", nil }, "linux")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "json", cfg.Defaults.LogFormat)
	require.Equal(t, 30*time.Second, cfg.Defaults.PollInterval)
	require.Equal(t, "gruvbox-dark", cfg.Theme.Name)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "prod-vanilla", cfg.Backends[0].Name)
	require.Equal(t, "secret-prod", cfg.Backends[0].BearerToken,
		"env var must be substituted into the bearer token")
}

func TestLoad_UnsetEnvVarErrors(t *testing.T) {
	t.Parallel()

	dir := writeFixtureToDir(t, stagingFixture)
	env := func(string) string { return "" }

	_, err := loadWithEnv(LoadOpts{Dir: dir}, env, func() (string, error) { return "/u", nil }, "linux")
	require.Error(t, err, "unresolved ${VAR} must surface as an error")
}

func TestLoad_NoFileReturnsErrNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := func(string) string { return "" }

	_, err := loadWithEnv(LoadOpts{Dir: dir}, env, func() (string, error) { return "/u", nil }, "linux")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound,
		"missing config file must surface ErrNotFound so the wizard caller can branch")
}

func TestLoad_MissingDirAlsoReturnsErrNotFound(t *testing.T) {
	t.Parallel()

	// Pinning the deliberate conflation documented on ErrNotFound:
	// a missing parent directory and a missing file produce the same
	// sentinel so the wizard caller can use one branch.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	env := func(string) string { return "" }

	_, err := loadWithEnv(LoadOpts{Dir: missing}, env, func() (string, error) { return "/u", nil }, "linux")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound, "missing config dir must surface ErrNotFound")
}

func TestLoad_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	dir := writeFixtureToDir(t, "backends:\n  - name: typo\n    url: http://x\n    pollInterval: 30s\n")
	env := func(string) string { return "" }

	_, err := loadWithEnv(LoadOpts{Dir: dir}, env, func() (string, error) { return "/u", nil }, "linux")
	require.Error(t, err, "strict-mode decoder must reject unknown fields")
	require.NotErrorIs(t, err, ErrNotFound, "this is a parse error, not a missing-file error")
}

func TestLoad_ResolvesConfigDirFromEnv(t *testing.T) {
	t.Parallel()

	dir := writeFixtureToDir(t, "backends:\n  - name: x\n    url: http://x\n")
	env := func(k string) string {
		if k == envConfigDir {
			return dir
		}
		return ""
	}

	cfg, err := loadWithEnv(LoadOpts{}, env, func() (string, error) { return "/u", nil }, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "x", cfg.Backends[0].Name)
}

func TestLoad_ExplicitDirWinsOverEnv(t *testing.T) {
	t.Parallel()

	good := writeFixtureToDir(t, "backends:\n  - name: from-explicit\n    url: http://x\n")
	bogus := t.TempDir() // contains nothing
	env := func(k string) string {
		if k == envConfigDir {
			return bogus
		}
		return ""
	}

	cfg, err := loadWithEnv(LoadOpts{Dir: good}, env, func() (string, error) { return "/u", nil }, "linux")
	require.NoError(t, err)
	require.Equal(t, "from-explicit", cfg.Backends[0].Name)
}

func TestLoad_CustomFileBasename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	custom := "alt.yaml"
	require.NoError(t, os.WriteFile(filepath.Join(dir, custom),
		[]byte("backends:\n  - name: alt\n    url: http://x\n"), 0o600))
	env := func(string) string { return "" }

	cfg, err := loadWithEnv(LoadOpts{Dir: dir, File: custom},
		env, func() (string, error) { return "/u", nil }, "linux")
	require.NoError(t, err)
	require.Equal(t, "alt", cfg.Backends[0].Name)
}

func TestLoad_PermissionErrorIsNotErrNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, defaultConfigFile)
	// Restore mode FIRST in the cleanup chain so t.TempDir's recursive
	// remove can delete the file even if the test panics mid-way; the
	// chmod-then-create-then-chmod-back dance is otherwise flaky on CI.
	require.NoError(t, os.WriteFile(path, []byte("backends: []\n"), 0o600))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	require.NoError(t, os.Chmod(path, 0o000))

	env := func(string) string { return "" }
	_, err := loadWithEnv(LoadOpts{Dir: dir}, env, func() (string, error) { return "/u", nil }, "linux")

	// Permission denied != not found; ErrNotFound branch must not
	// fire so the wizard does not eat a real I/O error.
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound, "permission errors must not surface as ErrNotFound")
}

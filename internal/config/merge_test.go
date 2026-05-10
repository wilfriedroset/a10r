// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// goosWindows is the runtime.GOOS value used to skip symlink-creation
// tests on Windows, where os.Symlink requires admin privilege. The
// unix CI is the contract; the skip is a pragmatic guard for local
// `go test` runs on non-CI Windows hosts.
const goosWindows = "windows"

// envNone is a getenv stub that returns empty for every key. The
// loader rejects any unresolved ${VAR} so fixtures in this file stay
// literal — tests that exercise interpolation live in interpolate_test.go.
func envNone(string) string { return "" }

// homeNone is a homeDir stub. Drop-in tests do not need a real home
// directory because every call site sets opts.Dir explicitly.
func homeNone() (string, error) { return "/u", nil }

// writeBaseAndDropIns lays out a tempdir with a10r.yaml and the
// supplied drop-in fragments under config.d/. Returns the resolved
// base directory.
func writeBaseAndDropIns(t *testing.T, base string, dropIns map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, defaultConfigFile), []byte(base), 0o600))
	if len(dropIns) == 0 {
		return dir
	}
	dDir := filepath.Join(dir, configDropInDir)
	require.NoError(t, os.MkdirAll(dDir, 0o700))
	for name, body := range dropIns {
		full := filepath.Join(dDir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	return dir
}

func TestLoad_DropIn_NoConfigD_IsNoOp(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		nil)

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
	require.Equal(t, "base", cfg.Backends[0].Name)
}

func TestLoad_DropIn_EmptyConfigD_IsNoOp(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		nil)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, configDropInDir), 0o700))

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 1)
}

func TestLoad_DropIn_AppendsBackend(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://base\n",
		map[string]string{
			"10-staging.yaml": "backends:\n  - name: staging\n    url: http://staging\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 2)
	require.Equal(t, "base", cfg.Backends[0].Name)
	require.Equal(t, "staging", cfg.Backends[1].Name)
}

func TestLoad_DropIn_LexicalOrderLastWinsForScalars(t *testing.T) {
	t.Parallel()

	// 10-* sets poll_interval to 30s; 20-* overrides to 60s. The
	// lexically last drop-in wins for scalars per the documented
	// merge contract — same as systemd .d/ overrides.
	dir := writeBaseAndDropIns(t,
		"defaults:\n  poll_interval: 5s\n",
		map[string]string{
			"20-second.yaml": "defaults:\n  poll_interval: 60s\n",
			"10-first.yaml":  "defaults:\n  poll_interval: 30s\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Equal(t, 60*time.Second, cfg.Defaults.PollInterval,
		"lexically-last drop-in must win for scalar fields")
}

func TestLoad_DropIn_PreservesUnsetBaseFields(t *testing.T) {
	t.Parallel()

	// A drop-in that sets only one field under defaults must not
	// nuke the other defaults from the base. This is the contract
	// that distinguishes per-field merge from "replace whole struct".
	dir := writeBaseAndDropIns(t,
		"defaults:\n  poll_interval: 30s\n  log_format: json\n",
		map[string]string{
			"10-overrides.yaml": "defaults:\n  poll_interval: 60s\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Equal(t, 60*time.Second, cfg.Defaults.PollInterval)
	require.Equal(t, "json", cfg.Defaults.LogFormat,
		"unset overlay fields must leave base values intact")
}

func TestLoad_DropIn_RecursiveDiscovery(t *testing.T) {
	t.Parallel()

	// Drop-ins under nested directories must still be picked up so
	// ops teams can group fragments by environment, e.g. config.d/prod/.
	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://base\n",
		map[string]string{
			"prod/10-tenant.yaml":    "backends:\n  - name: prod-tenant\n    url: http://prod\n",
			"staging/10-tenant.yaml": "backends:\n  - name: staging-tenant\n    url: http://staging\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 3)
	names := []string{cfg.Backends[0].Name, cfg.Backends[1].Name, cfg.Backends[2].Name}
	require.Contains(t, names, "base")
	require.Contains(t, names, "prod-tenant")
	require.Contains(t, names, "staging-tenant")
}

func TestLoad_DropIn_DuplicateBackendNameFailsClosed(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: prod\n    url: http://base\n",
		map[string]string{
			"10-extra.yaml": "backends:\n  - name: prod\n    url: http://other\n",
		})

	_, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.Error(t, err, "duplicate backend name across sources must fail closed")
	require.ErrorContains(t, err, "duplicate backend name")
	require.ErrorContains(t, err, "prod")
	require.ErrorContains(t, err, defaultConfigFile,
		"error must name the base file so the operator can find the original")
	require.ErrorContains(t, err, "10-extra.yaml",
		"error must name the offending drop-in")
}

func TestLoad_DropIn_DuplicateAcrossDropInsFailsClosed(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends: []\n",
		map[string]string{
			"10-first.yaml":  "backends:\n  - name: shared\n    url: http://a\n",
			"20-second.yaml": "backends:\n  - name: shared\n    url: http://b\n",
		})

	_, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.Error(t, err)
	require.ErrorContains(t, err, "duplicate backend name")
	require.ErrorContains(t, err, "shared")
	require.ErrorContains(t, err, "10-first.yaml")
	require.ErrorContains(t, err, "20-second.yaml")
}

func TestLoad_DropIn_StrictModeRejectsTypos(t *testing.T) {
	t.Parallel()

	// Drop-ins go through the same strict-mode discipline as the
	// base file — a typo in a snippet must surface at startup, not
	// silently drop into a wrong default.
	dir := writeBaseAndDropIns(t,
		"backends: []\n",
		map[string]string{
			"10-typo.yaml": "backends:\n  - name: x\n    url: http://x\n    pollInterval: 30s\n",
		})

	_, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.Error(t, err, "strict-mode must reject unknown fields in drop-ins")
	require.ErrorContains(t, err, "10-typo.yaml")
}

func TestLoad_DropIn_EmptyFragmentSkipped(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://base\n",
		map[string]string{
			"10-empty.yaml":   "",
			"20-comment.yaml": "# placeholder, fill in later\n",
			"30-real.yaml":    "backends:\n  - name: real\n    url: http://real\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err, "empty / comment-only drop-ins must be a no-op, not a parse error")
	require.Len(t, cfg.Backends, 2)
}

func TestLoad_DropIn_SkipsNonYAMLFiles(t *testing.T) {
	t.Parallel()

	// Editor swap files, README hints, and other artefacts that
	// happen to live under config.d/ must not be parsed as YAML
	// fragments — the suffix filter is the gate.
	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		map[string]string{
			"README.md":       "# this is not yaml",
			".10-snippet.swp": "garbage: not parseable",
			"10-snippet.yaml": "backends:\n  - name: real\n    url: http://x\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 2)
}

func TestLoad_DropIn_AcceptsBothYAMLAndYML(t *testing.T) {
	t.Parallel()

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		map[string]string{
			"10-a.yaml": "backends:\n  - name: a\n    url: http://a\n",
			"20-b.yml":  "backends:\n  - name: b\n    url: http://b\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 3)
}

func TestLoad_DropIn_FollowsSymlinkedFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == goosWindows {
		t.Skip("symlinks require admin privilege on Windows; the unix CI is the contract")
	}

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		nil)

	// Stage the real fragment under a sibling directory and symlink
	// it into config.d/. This mirrors the documented use-case: ops
	// stages snippets under /etc/a10r/snippets/ and links them into
	// the user's config.d/ via configuration management.
	staged := filepath.Join(dir, "staged")
	require.NoError(t, os.MkdirAll(staged, 0o700))
	target := filepath.Join(staged, "tenant.yaml")
	require.NoError(t, os.WriteFile(target,
		[]byte("backends:\n  - name: linked\n    url: http://linked\n"), 0o600))

	dDir := filepath.Join(dir, configDropInDir)
	require.NoError(t, os.MkdirAll(dDir, 0o700))
	require.NoError(t, os.Symlink(target, filepath.Join(dDir, "10-link.yaml")))

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 2)
	require.Equal(t, "linked", cfg.Backends[1].Name,
		"symlink target must be read transparently")
}

func TestLoad_DropIn_FollowsSymlinkedDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == goosWindows {
		t.Skip("symlinks require admin privilege on Windows")
	}

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		nil)

	// Stage two fragments outside config.d/ and link the directory
	// into config.d/snippets. The walker must descend through the
	// symlinked directory.
	staged := filepath.Join(dir, "staged")
	require.NoError(t, os.MkdirAll(staged, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(staged, "tenant-a.yaml"),
		[]byte("backends:\n  - name: a\n    url: http://a\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(staged, "tenant-b.yaml"),
		[]byte("backends:\n  - name: b\n    url: http://b\n"), 0o600))

	dDir := filepath.Join(dir, configDropInDir)
	require.NoError(t, os.MkdirAll(dDir, 0o700))
	require.NoError(t, os.Symlink(staged, filepath.Join(dDir, "snippets")))

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 3)
}

func TestLoad_DropIn_SymlinkLoopShortCircuits(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == goosWindows {
		t.Skip("symlinks require admin privilege on Windows")
	}

	dir := writeBaseAndDropIns(t,
		"backends:\n  - name: base\n    url: http://x\n",
		map[string]string{
			"10-real.yaml": "backends:\n  - name: real\n    url: http://real\n",
		})

	dDir := filepath.Join(dir, configDropInDir)
	// Self-loop: config.d/loop -> config.d/. A naive walker would
	// recurse forever; the visited-set must short-circuit silently
	// so the merge still completes with the real fragment in place.
	require.NoError(t, os.Symlink(dDir, filepath.Join(dDir, "loop")))

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Len(t, cfg.Backends, 2)
}

func TestLoad_DropIn_FailsValidationOnMergedResult(t *testing.T) {
	t.Parallel()

	// A drop-in that brings an invalid backend (two auth blocks)
	// must trip Validate even though the base file alone would
	// pass — the validator runs on the merged Config so
	// cross-source invariants are caught.
	dir := writeBaseAndDropIns(t,
		"backends: []\n",
		map[string]string{
			"10-bad.yaml": "backends:\n  - name: bad\n    url: http://x\n    bearer_token: t\n    basic_auth:\n      username: u\n      password: p\n",
		})

	_, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.Error(t, err)
	require.ErrorContains(t, err, "at most one of")
}

func TestLoad_DropIn_PageOverridesMerge(t *testing.T) {
	t.Parallel()

	// Per-page overrides under pages.<name>.poll_interval must
	// merge with the same per-field semantics: a drop-in that
	// only touches pages.alerts must not erase pages.silences
	// from the base.
	dir := writeBaseAndDropIns(t,
		"pages:\n  alerts:\n    poll_interval: 5s\n  silences:\n    poll_interval: 10s\n",
		map[string]string{
			"10-page.yaml": "pages:\n  alerts:\n    poll_interval: 2s\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.Equal(t, 2*time.Second, cfg.Pages.Alerts.PollInterval)
	require.Equal(t, 10*time.Second, cfg.Pages.Silences.PollInterval,
		"unrelated page entries must survive the merge")
}

func TestLoad_DropIn_TUITipsOneWayWins(t *testing.T) {
	t.Parallel()

	// tui.tips defaults to false ("no scouted features without
	// explicit go") and behaves the same one-way as ReadOnly: a
	// drop-in that flips it to true wins over a base default of
	// false. TipsInterval is plain non-zero-wins so a drop-in can
	// override the cadence without touching the on/off flag.
	dir := writeBaseAndDropIns(t,
		"tui:\n  tips: false\n  tips_interval: 5s\n",
		map[string]string{
			"10-tips.yaml": "tui:\n  tips: true\n  tips_interval: 12s\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.True(t, cfg.TUI.Tips)
	require.Equal(t, 12*time.Second, cfg.TUI.TipsInterval,
		"drop-in cadence override must reach the resolved config")
}

func TestLoad_DropIn_ReadOnlyOneWayWins(t *testing.T) {
	t.Parallel()

	// Read-only is one-way (any-true wins) per the documented
	// "Read-only mode" contract. A drop-in setting it to true must
	// override a base default of false; the inverse should not be
	// reachable through this path.
	dir := writeBaseAndDropIns(t,
		"defaults:\n  read_only: false\n",
		map[string]string{
			"10-lockdown.yaml": "defaults:\n  read_only: true\n",
		})

	cfg, err := loadWithEnv(LoadOpts{Dir: dir}, envNone, homeNone, "linux")
	require.NoError(t, err)
	require.True(t, cfg.Defaults.ReadOnly)
}

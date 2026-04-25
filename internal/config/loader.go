// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// ErrNotFound is returned by Load when no config file (or, by
// extension, no parent config directory) exists at the resolved
// path. The conflation is deliberate: a fresh user with no
// `~/.config/a10r/` directory is in the same UX state as one whose
// dir exists but `a10r.yaml` does not — the wizard runs in either
// case. Callers check via errors.Is to decide whether to prompt,
// run with defaults, or surface a hard error.
var ErrNotFound = errors.New("config file not found")

// LoadOpts directs config resolution. All fields are optional;
// defaults reproduce the K1 / B2 precedence:
//
//   - Dir: explicit (CLI flag) > A10R_CONFIG_DIR env > OS XDG default.
//   - File: a10r.yaml inside the resolved directory.
type LoadOpts struct {
	// Dir is the explicit config directory. When empty, resolution
	// falls back to A10R_CONFIG_DIR then DefaultDir.
	Dir string
	// File is the basename of the config file inside Dir. Empty means
	// the canonical "a10r.yaml".
	File string
}

// Load reads, env-interpolates, and parses the config file at the
// resolved path. Strict-mode YAML decoding is enabled per the
// contract pinned in TestConfig_StrictModeRejectsUnknownFields, so
// typos in field names surface as errors rather than silently
// dropping into a wrong default.
//
// Returns ErrNotFound when the file does not exist; the caller
// decides whether to launch the first-run wizard or fail.
func Load(opts LoadOpts) (*Config, error) {
	return loadWithEnv(opts, hostGetenv, hostHomeDir, hostGOOS())
}

// loadWithEnv is the test-injectable core of Load. Production code
// always reaches it via Load (which passes the host's env, home,
// and GOOS). Tests pass deterministic stubs to drive every branch
// without touching real env state.
func loadWithEnv(
	opts LoadOpts,
	env func(string) string,
	homeDir func() (string, error),
	goos string,
) (*Config, error) {
	dir, err := resolveConfigDir(opts.Dir, env, homeDir, goos)
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}

	file := opts.File
	if file == "" {
		file = defaultConfigFile
	}
	path := filepath.Join(dir, file)

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	interpolated, err := interpolateBytes(raw, env)
	if err != nil {
		return nil, fmt.Errorf("env interpolation in %q: %w", path, err)
	}

	cfg, err := decodeStrict(interpolated)
	if err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}

// decodeStrict runs a strict-mode YAML decode of the (already
// interpolated) byte stream. Pulled out so the loader stays flat
// and so tests can drive the strict-mode branch without going
// through the full filesystem path.
//
// The internal yaml decode error is returned unwrapped because
// loadWithEnv already adds the `parse config %q: %w` wrapper at the
// only production call site; double-wrapping would just produce
// `parse config "...": strict decode: yaml: ...`.
func decodeStrict(b []byte) (*Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err //nolint:wrapcheck // wrapping happens at the loader's call site (see godoc above)
	}
	return &cfg, nil
}

// hostGetenv, hostHomeDir, hostGOOS are package-level wrappers around
// the host's runtime so the test seams in loadWithEnv stay typed.
// They are referenced — and only referenced — by Load and DefaultDir.
func hostGetenv(name string) string { return os.Getenv(name) }
func hostHomeDir() (string, error)  { return os.UserHomeDir() }
func hostGOOS() string              { return runtime.GOOS }

// ResolveDir returns the directory Load would consult under the K1
// precedence (explicit > A10R_CONFIG_DIR env > OS default), without
// touching the filesystem. Useful for diagnostics like `a10r info`
// that need to display the location even when the config file does
// not exist.
func ResolveDir(explicit string) (string, error) {
	return resolveConfigDir(explicit, hostGetenv, hostHomeDir, hostGOOS())
}

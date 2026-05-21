// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
// defaults reproduce the ADR 0027 precedence:
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

// loadWithEnv is the test-injectable core of Load; production
// reaches it via Load with the host's env, home, and GOOS.
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

	cfg, err := loadOneFile(path, env)
	if err != nil {
		return nil, err
	}

	if err := mergeDropIns(cfg, filepath.Join(dir, configDropInDir), path, env); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return cfg, nil
}

// loadOneFile reads, env-interpolates, and strict-decodes the base
// config file into a Config. It surfaces ErrNotFound when the file is
// missing so the wizard caller can branch. Drop-in fragments under
// config.d/ go through loadDropIn instead — their empty-file and
// not-found semantics differ.
func loadOneFile(path string, env func(string) string) (*Config, error) {
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

// mergeDropIns folds every *.yaml / *.yml under dropInDir onto base,
// in lexical order. Each fragment is read, env-interpolated, and
// strict-decoded with the same discipline as the base file so a
// typo in a snippet surfaces at startup rather than producing a
// silently-wrong config. Backends are tracked across fragments so
// the duplicate-name error can name BOTH source files.
//
// A missing config.d/ is a no-op: operators who do not curate
// drop-ins pay nothing. Empty / comment-only fragments are also
// skipped — operators stage placeholder snippets via configuration
// management and they should not crash startup before being filled in.
func mergeDropIns(base *Config, dropInDir, basePath string, env func(string) string) error {
	paths, err := discoverDropIns(dropInDir)
	if err != nil {
		return fmt.Errorf("discover drop-ins: %w", err)
	}
	if len(paths) == 0 {
		return nil
	}

	backendSource := make(map[string]string, len(base.Backends)+len(paths))
	for i := range base.Backends {
		backendSource[base.Backends[i].Name] = basePath
	}

	for _, p := range paths {
		overlay, err := loadDropIn(p, env)
		if err != nil {
			return err
		}
		if err := mergeInto(base, overlay, p, backendSource); err != nil {
			return err
		}
	}
	return nil
}

// loadDropIn reads, env-interpolates, and strict-decodes a single
// drop-in fragment. Empty / comment-only fragments resolve to a
// zero-value *Config rather than nil — that lets the caller skip a
// nil-check and the zero overlay merges as a no-op (no backends,
// no scalar overrides) by construction.
//
// The base load uses loadOneFile instead because the missing-file and
// empty-file semantics differ: the base file's ErrNotFound branches
// to the wizard, and an empty base file is still a hard parse error
// to preserve the strict-mode contract documented on TestLoad_*.
func loadDropIn(path string, env func(string) string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read drop-in %q: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return &Config{}, nil
	}
	interpolated, err := interpolateBytes(raw, env)
	if err != nil {
		return nil, fmt.Errorf("env interpolation in %q: %w", path, err)
	}
	cfg, err := decodeStrict(interpolated)
	if err != nil {
		if errors.Is(err, io.EOF) {
			// Comment-only fragments survive the TrimSpace check above
			// (the comments are non-whitespace bytes) but the decoder
			// produces no document — treat the same as empty.
			return &Config{}, nil
		}
		return nil, fmt.Errorf("parse drop-in %q: %w", path, err)
	}
	return cfg, nil
}

// decodeStrict runs a strict-mode YAML decode of the (already
// interpolated) byte stream. The yaml error is returned unwrapped
// because loadWithEnv adds the `parse config %q: %w` wrapper at
// the only production call site.
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

// ResolveDir returns the directory Load would consult under the
// ADR 0027 precedence (explicit > A10R_CONFIG_DIR env > OS default),
// without touching the filesystem. Useful for diagnostics like
// `a10r info` that need to display the location even when the
// config file does not exist.
func ResolveDir(explicit string) (string, error) {
	return resolveConfigDir(explicit, hostGetenv, hostHomeDir, hostGOOS())
}

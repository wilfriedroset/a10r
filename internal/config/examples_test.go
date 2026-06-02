// SPDX-License-Identifier: Apache-2.0

package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExamples_AllParseAndValidate guards every shipped example
// under `../../examples/` against schema drift. Any future schema
// change must update the examples in the same commit so the
// documentation remains executable.
//
// Files that don't begin with `backends:` are not a10r configs
// (e.g. examples/alertmanager.yml is the upstream Alertmanager's
// own config) and the test silently skips them.
func TestExamples_AllParseAndValidate(t *testing.T) {
	t.Parallel()

	matches, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "examples dir must not be empty")

	// Every secret referenced by a shipped example must resolve to a
	// non-empty stub so loadWithEnv's `${VAR}` interpolation
	// succeeds. Maintaining this map next to the test (rather than
	// pulling from os.Environ) keeps the test hermetic.
	stub := func(string) string { return "stub" }

	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			dir := filepath.Dir(path)
			file := filepath.Base(path)

			cfg, err := loadWithEnv(LoadOpts{Dir: dir, File: file},
				stub, func() (string, error) { return "/u", nil }, "linux")
			require.NoError(t, err, "example %s must load through the strict-mode loader", file)
			require.NotNil(t, cfg)
		})
	}
}

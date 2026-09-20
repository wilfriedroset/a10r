// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The `:config` page answers "which file set this", so the order of
// Sources is the contract: base first, then the drop-ins in the same
// lexical order the merge applied them.
func TestLoad_SourcesFollowTheMergeOrder(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(base,
		[]byte("backends:\n  - name: a\n    url: http://a\n"), 0o600))

	dropInDir := filepath.Join(dir, "config.d")
	require.NoError(t, os.MkdirAll(dropInDir, 0o700))
	second := filepath.Join(dropInDir, "20-second.yaml")
	first := filepath.Join(dropInDir, "10-first.yaml")
	require.NoError(t, os.WriteFile(second,
		[]byte("backends:\n  - name: c\n    url: http://c\n"), 0o600))
	require.NoError(t, os.WriteFile(first,
		[]byte("backends:\n  - name: b\n    url: http://b\n"), 0o600))

	cfg, err := Load(LoadOpts{Dir: dir})
	require.NoError(t, err)
	require.Equal(t, []Source{
		{Kind: SourceBase, Path: base},
		{Kind: SourceDropIn, Path: first},
		{Kind: SourceDropIn, Path: second},
	}, cfg.Sources)
}

func TestLoad_SourcesHoldTheBaseFileAloneWithoutDropIns(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(base,
		[]byte("backends:\n  - name: a\n    url: http://a\n"), 0o600))

	cfg, err := Load(LoadOpts{Dir: dir})
	require.NoError(t, err)
	require.Equal(t, []Source{{Kind: SourceBase, Path: base}}, cfg.Sources)
}

// Sources is a load result, not a config key. A user file that names
// it must fail the strict decode like any other unknown field.
func TestLoad_SourcesIsNotAUserSettableKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a10r.yaml"),
		[]byte("sources:\n  - kind: base\n    path: /x\n"), 0o600))

	_, err := Load(LoadOpts{Dir: dir})
	require.ErrorContains(t, err, "field sources not found")
}

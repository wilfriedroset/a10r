// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/uistate"
	"github.com/wilfriedroset/a10r/internal/xdg"
)

// info is a diagnostic, so opening the state store must leave the
// file byte-for-byte alone. A later flush-on-Close or normalize-on-
// Open inside internal/uistate would otherwise make `a10r info`
// rewrite a hand-edited file with nothing failing.
func TestRunInfo_LeavesTheStateFileAlone(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv(xdg.StateHome, stateHome)

	stateDir := filepath.Join(stateHome, "a10r")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	statePath := filepath.Join(stateDir, uistate.FileName)
	// Deliberately not what yaml.Marshal emits: a comment and a
	// 2-space indent both parse fine and both die in a rewrite, so
	// the byte comparison below fails on a flush info must never do.
	body := "# hand-edited\nscope: prod\nsort:\n  alerts: severity:asc\n"
	require.NoError(t, os.WriteFile(statePath, []byte(body), 0o600))

	cfgDir := t.TempDir()
	writeYAML(t, cfgDir, "a10r.yaml",
		"backends:\n  - name: prod\n    url: http://x\ntui:\n  remember: true\n")

	var buf bytes.Buffer
	require.NoError(t, runInfo(&buf, &GlobalFlags{
		ConfigDir: cfgDir,
		LogPath:   filepath.Join(stateDir, "a10r.log"),
	}))
	require.Contains(t, buf.String(), "scope:      prod (remembered)")

	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	require.Equal(t, body, string(after))

	tmps, err := filepath.Glob(filepath.Join(stateDir, "*.tmp"))
	require.NoError(t, err)
	require.Empty(t, tmps)
}

// A remembered tenant the config dropped must not reach the report:
// info answers "why did a10r open there", and boot prunes the same
// name away before it ever opens.
func TestRunInfo_PrunesTheRememberedScope(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv(xdg.StateHome, stateHome)

	stateDir := filepath.Join(stateHome, "a10r")
	require.NoError(t, os.MkdirAll(stateDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(stateDir, uistate.FileName), []byte("scope: gone\n"), 0o600,
	))

	cfgDir := t.TempDir()
	writeYAML(t, cfgDir, "a10r.yaml",
		"backends:\n  - name: prod\n    url: http://x\ntui:\n  remember: true\n")

	var buf bytes.Buffer
	require.NoError(t, runInfo(&buf, &GlobalFlags{
		ConfigDir: cfgDir,
		LogPath:   filepath.Join(stateDir, "a10r.log"),
	}))
	require.NotContains(t, buf.String(), "gone")
	require.NotContains(t, buf.String(), "(remembered)")
}

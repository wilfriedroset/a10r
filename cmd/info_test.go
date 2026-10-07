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

// `a10r info` and the TUI's `:info` page print one report, so they
// must agree on the log path and the skin for one config file. The
// TUI resolves both through config.Resolve; info reading its raw
// flag instead printed the default log path and the default theme
// for a file that set either one.
func TestRunInfo_ReportsTheResolvedLogPathAndTheme(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv(xdg.StateHome, stateHome)
	// config.Resolve reads the environment now, and A10R_LOG outranks
	// the file, so a developer host that exports it would otherwise
	// fail this test for the wrong reason.
	t.Setenv("A10R_LOG", "")
	t.Setenv("A10R_READ_ONLY", "")

	cfgDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "from-config.log")
	writeYAML(t, cfgDir, "a10r.yaml",
		"backends:\n  - name: prod\n    url: http://x\n"+
			"log:\n  path: "+logPath+"\n"+
			"theme:\n  name: catppuccin-latte\n")

	var buf bytes.Buffer
	require.NoError(t, runInfo(&buf, &GlobalFlags{ConfigDir: cfgDir}))

	require.Contains(t, buf.String(), "log path:   "+logPath)
	require.Contains(t, buf.String(), "theme:      catppuccin-latte")
}

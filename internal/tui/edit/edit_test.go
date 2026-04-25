// SPDX-License-Identifier: Apache-2.0

package edit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"
)

const windowsGOOS = "windows"

// syncRunner runs the exec.Cmd synchronously and produces the
// callback's tea.Msg — exactly what tea.ExecProcess does inside
// the bubbletea program loop, but accessible from tests.
func syncRunner(c *exec.Cmd, fn func(error) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return fn(c.Run())
	}
}

// makeEditorScript writes a tiny shell script that appends a known
// suffix to the file the editor is invoked with, then exits 0.
// Used to drive tea.ExecProcess substitutes in tests.
func makeEditorScript(t *testing.T, suffix string) string {
	t.Helper()
	if runtime.GOOS == windowsGOOS {
		t.Skip("script-based editor test relies on POSIX sh")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-editor")
	body := "#!/bin/sh\nprintf '%s' \"" + suffix + "\" >> \"$1\"\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

func TestResolver_EditorPicksFirstNonEmptyEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"A10R_EDITOR": "vim -u NONE",
		"EDITOR":      "nano",
	}
	r := Resolver{
		EditorEnv:     []string{"A10R_EDITOR", "EDITOR"},
		DefaultEditor: "vi",
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}
	require.Equal(t, []string{"vim", "-u", "NONE"}, r.Editor())
}

func TestResolver_EditorFallsThroughEmptyEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"A10R_EDITOR": "",
		"EDITOR":      "nano",
	}
	r := Resolver{
		EditorEnv:     []string{"A10R_EDITOR", "EDITOR"},
		DefaultEditor: "vi",
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}
	require.Equal(t, []string{"nano"}, r.Editor(),
		"empty env value must fall through to the next key")
}

func TestResolver_EditorFallsBackToDefault(t *testing.T) {
	t.Parallel()

	r := Resolver{
		EditorEnv:     []string{"A10R_EDITOR", "EDITOR"},
		DefaultEditor: "vi",
		LookupEnv:     func(string) (string, bool) { return "", false },
	}
	require.Equal(t, []string{"vi"}, r.Editor())
}

func TestResolver_DefaultPlatformEditor(t *testing.T) {
	t.Parallel()

	r := SystemResolver()
	if runtime.GOOS == windowsGOOS {
		require.Equal(t, "notepad", r.DefaultEditor)
		return
	}
	require.Equal(t, "vi", r.DefaultEditor)
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"sil-42":         "sil-42",
		"weird/path|key": "weird_path_key",
		"":               "draft",
		"naïve\xff":      "na_ve_",
	}
	for in, want := range cases {
		require.Equal(t, want, sanitize(in),
			"sanitize(%q)", in)
	}
}

func TestEdit_RunsScriptAndCapturesContent(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("ExecProcess test relies on a POSIX shell script")
	}
	cache := t.TempDir()
	editorPath := makeEditorScript(t, "appended-by-fake-editor")
	r := Resolver{
		EditorEnv:     []string{"A10R_EDITOR"},
		DefaultEditor: editorPath,
		CacheDir:      cache,
		LookupEnv:     func(string) (string, bool) { return "", false },
		ExecRunner:    syncRunner,
	}

	cmd := r.Edit(Request{ResourceID: "sil-test", Initial: "original\n"})
	require.NotNil(t, cmd)

	// tea.ExecProcess returns a Cmd whose underlying msg is produced
	// only when bubbletea drives it. Bubbletea blocks the program
	// while the editor runs. Calling cmd() directly walks that
	// path: the *exec.Cmd is run synchronously and the callback
	// returns the FinishedMsg.
	msg := cmd()
	fin := msg.(FinishedMsg)
	require.NoError(t, fin.Err)
	require.Equal(t, "sil-test", fin.ResourceID)
	require.Equal(t, "original\nappended-by-fake-editor", fin.Content)

	// Tempfile was cleaned up.
	matches, _ := filepath.Glob(filepath.Join(cache, "edit-*"))
	require.Empty(t, matches, "tempfile must be removed after the editor exits")
}

func TestEdit_EditorErrorBubblesUp(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("relies on /bin/false")
	}
	cache := t.TempDir()
	r := Resolver{
		DefaultEditor: "/bin/false",
		CacheDir:      cache,
		LookupEnv:     func(string) (string, bool) { return "", false },
		ExecRunner:    syncRunner,
	}
	cmd := r.Edit(Request{ResourceID: "sil-fail", Initial: "x\n"})
	fin := cmd().(FinishedMsg)
	require.Error(t, fin.Err, "non-zero exit must surface as Err")
	require.Equal(t, "sil-fail", fin.ResourceID)
}

func TestEdit_DefaultIDProducesValidPath(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("relies on POSIX cat")
	}
	cache := t.TempDir()
	r := Resolver{
		DefaultEditor: "/bin/true", // exits 0, leaves file unchanged
		CacheDir:      cache,
		LookupEnv:     func(string) (string, bool) { return "", false },
		ExecRunner:    syncRunner,
	}
	cmd := r.Edit(Request{Initial: "hi"})
	fin := cmd().(FinishedMsg)
	require.NoError(t, fin.Err)
	require.Equal(t, "hi", fin.Content)
}

func TestEdit_NoEditorConfigured(t *testing.T) {
	t.Parallel()

	r := Resolver{
		DefaultEditor: "",
		CacheDir:      t.TempDir(),
		LookupEnv:     func(string) (string, bool) { return "", false },
	}
	cmd := r.Edit(Request{Initial: "x"})
	fin := cmd().(FinishedMsg)
	require.Error(t, fin.Err)
}

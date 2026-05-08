// SPDX-License-Identifier: Apache-2.0

package edit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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

// TestEdit_TempfileNameUsesRandomSuffix pins audit F10's
// resolution: each Edit call must produce a tempfile whose
// basename is unguessable from the resource id alone. Two calls
// with the same id must yield different paths so a co-tenant
// pre-creating a symlink at the obvious legacy path
// (`edit-<sanitized-id>.yaml`) cannot intercept the buffer.
func TestEdit_TempfileNameUsesRandomSuffix(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("relies on POSIX `true`")
	}
	cache := t.TempDir()

	// Capture the path of the tempfile each Edit produces by
	// reading the cache dir contents from inside a recording
	// ExecRunner. The runner doesn't actually exec; it records
	// the path argument the editor would have received.
	var capturedPaths []string
	runner := func(c *exec.Cmd, fn func(error) tea.Msg) tea.Cmd {
		require.NotEmpty(t, c.Args, "editor cmd must have arguments")
		capturedPaths = append(capturedPaths, c.Args[len(c.Args)-1])
		return func() tea.Msg { return fn(nil) }
	}
	r := Resolver{
		DefaultEditor: "/bin/true",
		CacheDir:      cache,
		LookupEnv:     func(string) (string, bool) { return "", false },
		ExecRunner:    runner,
	}
	cmd1 := r.Edit(Request{ResourceID: "sil-x", Initial: "a"})
	_ = cmd1()
	cmd2 := r.Edit(Request{ResourceID: "sil-x", Initial: "b"})
	_ = cmd2()
	require.Len(t, capturedPaths, 2)
	require.NotEqual(t, capturedPaths[0], capturedPaths[1],
		"two Edit() calls for the same resource must produce distinct tempfile paths so a symlink swap cannot target the next call")
	require.Contains(t, filepath.Base(capturedPaths[0]), "edit-sil-x-",
		"tempfile basename must still embed the sanitized id for operator-side debugging")
}

// TestEdit_RejectsSymlinkAtTempfilePath belt-and-braces the
// CreateTemp O_EXCL guard: even on a filesystem where O_EXCL is
// somehow misbehaving, an Lstat-detected non-regular file aborts
// the edit before exec'ing the editor. Tested by pre-populating
// the cache with a symlink at the deterministic legacy path AND
// configuring the resolver to use that path — guarded against
// by the random-suffix pattern, but the assertion proves the
// safety net exists.
func TestEdit_RejectsTempfileNotRegular(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == windowsGOOS {
		t.Skip("symlink semantics differ on windows")
	}
	// A path that exists as a symlink to /dev/null. The test
	// drives assertRegularFile directly because the resolver's
	// CreateTemp path is now random-suffix and unreachable from
	// a fixed pre-created symlink — the assertion proves the
	// helper itself rejects symlinks.
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink("/dev/null", link))
	err := assertRegularFile(link)
	require.ErrorIs(t, err, ErrTempfileNotRegular)
}

// TestEdit_CtxAbortsEditor pins audit F16: a cancelled parent
// ctx kills the editor subprocess. Without the fix, the editor
// inherited context.Background() and a parent shutdown couldn't
// abort a hung session.
func TestEdit_CtxAbortsEditor(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == windowsGOOS {
		t.Skip("relies on POSIX `sleep`")
	}
	cache := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	r := Resolver{
		DefaultEditor: "/bin/sleep 60", // hangs until killed
		CacheDir:      cache,
		LookupEnv:     func(string) (string, bool) { return "", false },
		ExecRunner:    syncRunner,
	}
	cmd := r.Edit(Request{ResourceID: "sil-z", Initial: "x", Ctx: ctx})
	// Cancel before draining the cmd so the in-flight editor
	// observes ctx.Done. syncRunner waits on cmd.Run(), which
	// returns once the SIGKILL lands.
	go func() {
		// Give exec a moment to start the child.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	fin := cmd().(FinishedMsg)
	require.Error(t, fin.Err, "cancelled ctx must surface a non-nil Err on FinishedMsg")
}

// TestEdit_CacheDirCreatedAt0o700 pins audit F10's cache-dir
// permission requirement: a fresh CacheDir is created mode 0o700
// so a local co-tenant cannot list / pre-populate the directory.
func TestEdit_CacheDirCreatedAt0o700(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == windowsGOOS {
		t.Skip("posix-mode assertions don't apply on windows")
	}
	parent := t.TempDir()
	cache := filepath.Join(parent, "fresh-cache")
	r := Resolver{
		DefaultEditor: "/bin/true",
		CacheDir:      cache,
		LookupEnv:     func(string) (string, bool) { return "", false },
		ExecRunner:    syncRunner,
	}
	cmd := r.Edit(Request{ResourceID: "id", Initial: "x"})
	_ = cmd()
	info, err := os.Stat(cache)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
		"cache dir must be created at 0o700 so a local co-tenant cannot list or pre-populate it")
}

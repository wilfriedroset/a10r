// SPDX-License-Identifier: Apache-2.0

// Package edit hands a buffer off to the user's external editor
// via tea.ExecProcess and returns the edited contents (or the
// error) as a tea.Msg.
//
// Editor resolution per the L1 plan:
//
//	$A10R_EDITOR  →  $EDITOR  →  "vi" (Linux/macOS)  /  "notepad" (Windows)
//
// Tempfile lives under $XDG_CACHE_HOME/a10r/ (or platform fallback)
// so the user can recover their draft if the editor crashes or
// they abort. The basename embeds the resource id so concurrent
// edits of different silences don't collide.
package edit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// FinishedMsg is delivered when the editor exits. Content is the
// post-edit buffer (whatever the user saved, or the original if
// they aborted without writing). Err is non-nil when the editor
// returned a non-zero status or the tempfile couldn't be read;
// Content is best-effort in that case.
type FinishedMsg struct {
	// ResourceID echoes back the identifier the caller passed in
	// Request.ResourceID so multi-resource flows can route the
	// result. Empty when the request didn't supply one.
	ResourceID string
	Content    string
	Err        error
}

// Request bundles the caller's intent.
type Request struct {
	// ResourceID is the silence (or future Mimir config) ID the
	// edit targets. Mixed into the tempfile basename so concurrent
	// edits don't clobber each other.
	ResourceID string
	// Initial is the buffer the editor opens with. Typically the
	// YAML-encoded current state of the resource.
	Initial string
	// Extension picks the tempfile extension so editor syntax
	// highlighting kicks in. Empty defaults to "yaml".
	Extension string
}

// Resolver injects the path lookups the package needs. Tests
// stub this to point at a recording editor / a controlled cache
// dir; the production wiring uses SystemResolver.
type Resolver struct {
	// EditorEnv is consulted in order — the first non-empty value
	// is treated as the user's editor command. Production passes
	// {"A10R_EDITOR", "EDITOR"}; tests may pass a single key to
	// pin the behaviour.
	EditorEnv []string
	// DefaultEditor is the fallback when no env var is set. For
	// production: "vi" on Unix, "notepad" on Windows. Tests can
	// override to point at a recording binary.
	DefaultEditor string
	// CacheDir is the parent for tempfiles. Empty defaults to
	// os.UserCacheDir()/a10r.
	CacheDir string
	// LookupEnv mirrors os.LookupEnv. nil falls back to os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// ExecRunner wraps the editor exec.Cmd so the result lands as
	// a tea.Msg. nil defaults to tea.ExecProcess. Tests inject a
	// runner that runs the cmd synchronously and emits the result
	// without going through bubbletea's program loop — tea.ExecProcess
	// returns an internal execMsg that only the program-loop
	// runtime knows how to dispatch, so it can't be unit-tested
	// without this injection point.
	ExecRunner func(*exec.Cmd, func(error) tea.Msg) tea.Cmd
}

// SystemResolver returns the production Resolver. Wraps os env
// access and picks a platform-appropriate default editor.
func SystemResolver() Resolver {
	def := "vi"
	if runtime.GOOS == "windows" {
		def = "notepad"
	}
	return Resolver{
		EditorEnv:     []string{"A10R_EDITOR", "EDITOR"},
		DefaultEditor: def,
	}
}

// Editor returns the editor command (e.g. "vim", "nano -w") the
// user has configured. The return is a slice so callers can fork
// arguments cleanly: ["nano", "-w"] vs ["vim"]. Returns an empty
// slice when no env var matches and DefaultEditor is also empty —
// callers must surface that as a user-facing error rather than
// trying to exec "".
func (r Resolver) Editor() []string {
	lookup := r.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	for _, key := range r.EditorEnv {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			return strings.Fields(v)
		}
	}
	if strings.TrimSpace(r.DefaultEditor) == "" {
		return nil
	}
	return strings.Fields(r.DefaultEditor)
}

// cacheRoot returns the tempfile parent directory, creating it
// if it doesn't exist.
func (r Resolver) cacheRoot() (string, error) {
	if r.CacheDir != "" {
		if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
			return "", err
		}
		return r.CacheDir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "a10r")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Edit returns the tea.Cmd that runs the editor. The Cmd writes
// the initial buffer to a tempfile, runs the editor against it,
// reads the post-edit content, removes the tempfile, and returns
// FinishedMsg.
//
// On any preparation error (tempfile create, editor exec) the
// returned Cmd produces a FinishedMsg with Err set and Content
// empty so the caller's Update can flash and stay on the form.
func (r Resolver) Edit(req Request) tea.Cmd {
	ext := req.Extension
	if ext == "" {
		ext = "yaml"
	}
	root, err := r.cacheRoot()
	if err != nil {
		return failed(req.ResourceID, err)
	}
	id := req.ResourceID
	if id == "" {
		id = "draft"
	}
	path := filepath.Join(root, "edit-"+sanitize(id)+"."+ext)
	if err := os.WriteFile(path, []byte(req.Initial), 0o600); err != nil {
		return failed(req.ResourceID, err)
	}
	editor := r.Editor()
	if len(editor) == 0 {
		return failed(req.ResourceID, errors.New("no editor configured"))
	}
	c := exec.CommandContext(context.Background(), editor[0], append(editor[1:], path)...) //nolint:gosec // editor command + tempfile path are trusted; the user controls $EDITOR.
	runner := r.ExecRunner
	if runner == nil {
		runner = func(cmd *exec.Cmd, fn func(error) tea.Msg) tea.Cmd {
			return tea.ExecProcess(cmd, tea.ExecCallback(fn))
		}
	}
	return runner(c, func(execErr error) tea.Msg {
		// Always read back the file — the editor may have written
		// content even on a non-zero exit (think `:cq` in vim).
		// If the read itself fails we return the editor error
		// (or the read error if there was no editor error).
		buf, readErr := os.ReadFile(path)
		_ = os.Remove(path)
		if execErr == nil {
			execErr = readErr
		}
		return FinishedMsg{
			ResourceID: req.ResourceID,
			Content:    string(buf),
			Err:        execErr,
		}
	})
}

// failed returns a Cmd that emits a FinishedMsg with the given
// error. Used for fast-fail prep paths.
func failed(id string, err error) tea.Cmd {
	return func() tea.Msg {
		return FinishedMsg{ResourceID: id, Err: err}
	}
}

// sanitize replaces filesystem-unfriendly characters so the
// resource id can land in a path. Conservative — keeps only
// alphanumerics, dot, hyphen, and underscore.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "draft"
	}
	return b.String()
}

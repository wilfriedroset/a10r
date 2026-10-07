// SPDX-License-Identifier: Apache-2.0

package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirMode is the permission applied to a state directory a10r
// creates. 0o700 keeps state files (which can leak the user's recent
// label-matcher queries) from any co-tenant on a shared host.
const DirMode = 0o700

// FileMode is the permission stamped on each state file. 0o600
// mirrors the dir's intent — owner-read, owner-write, no one else.
// A permissive umask cannot widen either constant, because umask
// only clears bits.
const FileMode = 0o600

// StateDir resolves a10r's state directory: `$XDG_STATE_HOME/a10r/`
// when the env var is set, else `$HOME/.local/state/a10r/` on unix.
// Mirrors the loader in internal/log/path.go so `a10r.log` and the
// other state files share one parent.
//
// env and homeDir are injected so the test suite can drive every
// branch from a single host without setenv contamination.
func StateDir(env func(string) string, homeDir func() (string, error)) (string, error) {
	if state := env(StateHome); state != "" {
		return filepath.Join(state, "a10r"), nil
	}
	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".local", "state", "a10r"), nil
}

// DefaultStateDir is the production path resolver — wraps StateDir
// with the live os.* functions. Callers that want dependency
// injection (tests) should call StateDir directly.
func DefaultStateDir() (string, error) {
	return StateDir(os.Getenv, os.UserHomeDir)
}

// AtomicWrite replaces path with data, creating the parent dir
// lazily. Atomic-by-rename so a SIGKILL mid-write can't corrupt the
// destination; any failure before the rename removes the temp file,
// so a caller retrying sees neither a stale temp nor a half-written
// path.
//
// The two flushes carry the write past a crash of the host rather
// than a crash of a10r alone. They are the last two steps, so an
// error from the directory flush means the new content is already
// at path and only its durability is in doubt.
func AtomicWrite(path string, data []byte) error {
	return atomicWriteWith(path, data, (*os.File).Sync)
}

// atomicWriteWith takes the flush as an argument so a test can
// refuse it.
func atomicWriteWith(path string, data []byte, fsync func(*os.File) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("state mkdir: %w", err)
	}
	// Pid-tagged temp name so two a10r instances writing the same
	// file at once can't shred each other's tmp mid-flight. The final
	// rename is still last-writer-wins on the destination — flocking
	// the path is overkill for a pet project where the realistic
	// concurrent-instance count is one.
	tmpPath := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, FileMode)
	if err != nil {
		return fmt.Errorf("state open tmp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("state write: %w", err)
	}
	if err := fsync(tmp); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("state sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("state close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("state rename: %w", err)
	}
	return syncDir(dir, fsync)
}

// SPDX-License-Identifier: Apache-2.0

package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Directory perms: 0o750 / 0o640. The skill carries no credentials, so it
// need not be as locked-down as a10r.yaml (0o600), but there is no reason to
// make an agent-instruction file world-readable either.
const (
	dirPerm  = 0o750
	filePerm = 0o640
)

// ResolveDir returns the skills base directory a10r installs into.
//
// Default is the vendor-neutral `~/.agents/skills` (read by Cursor, Gemini
// CLI, goose, pi, and others); --claude selects `~/.claude/skills`; --dest names an
// arbitrary directory. Claude does not auto-load the neutral location, which
// is why the caller nudges Claude users toward --claude. claude and dest are
// mutually exclusive — one destination per invocation, no symlink fan-out.
func ResolveDir(claude bool, dest string) (string, error) {
	if claude && dest != "" {
		return "", errors.New("--claude and --dest are mutually exclusive")
	}
	if dest != "" {
		return dest, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if claude {
		return filepath.Join(home, ".claude", "skills"), nil
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// TargetPath is where Install writes within a skills base directory. Exposed
// so the dry-run can report the destination without duplicating the layout.
func TargetPath(dir string) string {
	return filepath.Join(dir, SkillName, "SKILL.md")
}

// Install writes the embedded skill to <dir>/a10r/SKILL.md and returns the
// path written. It refuses to overwrite an existing skill unless force is
// set. The write is atomic (temp file + rename within the destination
// directory) so an interrupted install never leaves a half-written skill.
func Install(dir string, force bool) (string, error) {
	path := TargetPath(dir)
	skillDir := filepath.Dir(path)

	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("refusing to overwrite %s — pass --force to confirm", path)
		}
	}

	if err := os.MkdirAll(skillDir, dirPerm); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}
	if err := atomicWrite(path, content); err != nil {
		return "", err
	}
	return path, nil
}

// atomicWrite writes data via a temp file in path's directory and renames it
// into place, so a reader never observes a partial file and an interrupted
// install leaves no half-written skill. The temp file is removed on failure.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".SKILL-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

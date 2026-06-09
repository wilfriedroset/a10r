// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/skill"
)

func TestRunSkillsAdd_DryRunPrintsTargetWithoutWriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errBuf bytes.Buffer
	require.NoError(t, runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, DryRun: true}))

	target := filepath.Join(home, ".agents", "skills", "a10r", "SKILL.md")
	require.Contains(t, out.String(), target)
	require.NoFileExists(t, target)
}

func TestRunSkillsAdd_InstallsToNeutralAndNudgesClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errBuf bytes.Buffer
	require.NoError(t, runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf}))

	path := filepath.Join(home, ".agents", "skills", "a10r", "SKILL.md")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, skill.Content(), got)
	require.Contains(t, out.String(), path)
	require.Contains(t, out.String(), "(a10r "+version+")",
		"success line must record which a10r version installed the skill")
	require.Contains(t, errBuf.String(), "--claude",
		"installing to the neutral dir must nudge Claude users toward --claude")
}

func TestRunSkillsAdd_ClaudeInstallsWithoutNudge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out, errBuf bytes.Buffer
	require.NoError(t, runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, Claude: true}))

	require.FileExists(t, filepath.Join(home, ".claude", "skills", "a10r", "SKILL.md"))
	require.Empty(t, errBuf.String(), "no nudge when already installing for Claude")
}

func TestRunSkillsAdd_DestInstallsWithoutNudge(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	var out, errBuf bytes.Buffer
	require.NoError(t, runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, Dest: dest}))

	require.FileExists(t, filepath.Join(dest, "a10r", "SKILL.md"))
	require.Empty(t, errBuf.String())
}

func TestRunSkillsAdd_ClaudeAndDestIsError(t *testing.T) {
	t.Parallel()

	var out, errBuf bytes.Buffer
	err := runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, Claude: true, Dest: "/tmp/x"})
	require.Error(t, err)
	require.Equal(t, ExitRuntimeError, exitCodeFor(err))
}

func TestRunSkillsAdd_RefusesClobberThenForceOverwrites(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	var out, errBuf bytes.Buffer
	require.NoError(t, runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, Dest: dest}))

	err := runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, Dest: dest})
	require.Error(t, err)
	require.Equal(t, ExitRuntimeError, exitCodeFor(err))

	require.NoError(t, runSkillsAdd(skillsAddIO{Out: &out, Err: &errBuf, Dest: dest, Force: true}))
}

func TestSkillsPreview_PrintsEmbeddedVerbatim(t *testing.T) {
	t.Parallel()

	root := buildHelpRoot(t)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{skillsUse, "preview"})
	require.NoError(t, root.Execute())

	require.Equal(t, string(skill.Content()), buf.String())
}

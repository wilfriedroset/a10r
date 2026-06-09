// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/skill"
)

// skillsUse is the parent command verb, shared with the tests that invoke it.
const skillsUse = "skills"

// newSkillsCmd returns the `a10r skills` parent command: install and preview
// the embedded agent skill that teaches an AI assistant to drive a10r
// headless.
func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     skillsUse,
		Short:   "Install the a10r agent skill for an AI coding assistant",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newSkillsAddCmd(), newSkillsPreviewCmd())
	return cmd
}

// newSkillsAddCmd installs the embedded SKILL.md into an agent's skills
// directory. Default is the vendor-neutral ~/.agents/skills; --claude and
// --dest redirect it.
func newSkillsAddCmd() *cobra.Command {
	var claude, force, dryRun bool
	var dest string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Install the a10r agent skill into an AI assistant's skills directory",
		Example: `  # Vendor-neutral install (~/.agents/skills; read by Cursor, Gemini CLI, goose, pi, ...)
  a10r skills add

  # Install for Claude Code (~/.claude/skills)
  a10r skills add --claude

  # Install into a project-local or custom skills directory
  a10r skills add --dest .claude/skills`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSkillsAdd(skillsAddIO{
				Out:    cmd.OutOrStdout(),
				Err:    cmd.ErrOrStderr(),
				Claude: claude,
				Dest:   dest,
				Force:  force,
				DryRun: dryRun,
			})
		},
	}
	cmd.Flags().BoolVar(&claude, "claude", false,
		"install into ~/.claude/skills instead of the vendor-neutral ~/.agents/skills")
	cmd.Flags().StringVar(&dest, "dest", "",
		"install into a custom skills directory (mutually exclusive with --claude)")
	cmd.Flags().BoolVar(&force, "force", false,
		"overwrite an existing installed skill")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the install target and exit without writing anything")
	return cmd
}

// newSkillsPreviewCmd prints the embedded SKILL.md to stdout, so an agent can
// read the skill on demand without installing it.
func newSkillsPreviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preview",
		Short: "Print the embedded a10r skill (SKILL.md) to stdout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := cmd.OutOrStdout().Write(skill.Content()); err != nil {
				return fmt.Errorf("write skill: %w", err)
			}
			return nil
		},
	}
}

// skillsAddIO bundles runSkillsAdd's host handles and resolved flags so tests
// drive it with buffers instead of os.Stdout / the real filesystem layout.
type skillsAddIO struct {
	Out    io.Writer
	Err    io.Writer
	Claude bool
	Dest   string
	Force  bool
	DryRun bool
}

// runSkillsAdd resolves the destination, installs the embedded skill, and
// reports the path. A neutral-dir install nudges the user toward --claude,
// since Claude Code does not auto-load ~/.agents/skills. Failures carry
// ExitRuntimeError — this command touches no config or backend, so the
// config/backend exit codes do not apply.
func runSkillsAdd(env skillsAddIO) error {
	dir, err := skill.ResolveDir(env.Claude, env.Dest)
	if err != nil {
		return NewExitError(ExitRuntimeError, err)
	}

	if env.DryRun {
		fmt.Fprintf(env.Out, "would install a10r skill to %s\n", skill.TargetPath(dir))
		return nil
	}

	path, err := skill.Install(dir, env.Force)
	if err != nil {
		return NewExitError(ExitRuntimeError, err)
	}
	fmt.Fprintf(env.Out, "installed a10r skill (a10r %s) to %s\n", version, path)

	if !env.Claude && env.Dest == "" {
		fmt.Fprintln(env.Err,
			"note: Claude Code does not auto-load this location; "+
				"run `a10r skills add --claude` to install where Claude reads it.")
	}
	return nil
}

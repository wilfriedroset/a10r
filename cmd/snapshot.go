// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/tui/boot"
)

// snapshotUse is the command verb, shared with the tests that invoke it.
const snapshotUse = "snapshot"

// newSnapshotCmd returns the `a10r snapshot <page>` command: render
// one TUI frame to stdout and exit.
//
// Hidden from `--help` on purpose. It exists for the screenshot
// pipeline and for CI, not for on-callers, and every page it can
// render is one keystroke away inside the TUI itself.
//
// run is runSnapshot in production. Tests pass a recorder to read the
// options the flags produce.
func newSnapshotCmd(flags *GlobalFlags, run func(*cobra.Command, *GlobalFlags, boot.SnapshotOptions) error) *cobra.Command {
	var opts boot.SnapshotOptions
	cmd := &cobra.Command{
		Use:   snapshotUse + " <page>",
		Short: "Render one TUI frame to stdout (maintainer tool)",
		Long: `Render one frame of a TUI page to stdout and exit.

The frame is the assembled article: top panel, page body, and footer,
laid out for the requested size. a10r connects to every configured
backend, waits for the first poll, renders, and exits. A backend that
does not answer in time renders the same degraded band a user sees.

Pages: ` + strings.Join(boot.SnapshotPages(), ", "),
		// groupDiag even though Hidden keeps it out of the help:
		// cobra only skips ungrouped commands while they are
		// hidden, so unhiding this one later would drop it into
		// "Additional Commands" from a distance.
		GroupID: groupDiag,
		Hidden:  true,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Page = args[0]
			return run(cmd, flags, opts)
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.Width, "width", boot.DefaultSnapshotWidth, "frame width in terminal cells")
	f.IntVar(&opts.Height, "height", boot.DefaultSnapshotHeight, "frame height in terminal cells")
	f.BoolVar(&opts.Color, "color", false, "keep the skin's ANSI colors in the frame")
	return cmd
}

// runSnapshot boots the same startup graph the TUI runs, renders one
// frame, and writes it to stdout. Per ADR 0045 stdout carries only
// the frame; boot's startup warnings go to stderr.
func runSnapshot(cmd *cobra.Command, flags *GlobalFlags, opts boot.SnapshotOptions) error {
	res, err := boot.Build(cmd.Context(), flags, boot.Deps{
		Version:  version,
		Commit:   commit,
		Stderr:   cmd.ErrOrStderr(),
		Headless: true,
	})
	if err != nil {
		return fmt.Errorf("build TUI: %w", err)
	}
	defer res.Close()

	frame, err := res.Snapshot(cmd.Context(), opts)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), frame); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

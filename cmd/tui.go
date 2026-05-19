// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/wilfriedroset/a10r/internal/tui/boot"
)

// runTUI is the cobra RunE handed to the root command when no
// subcommand is supplied. The body is intentionally tiny: build
// the boot graph, defer the logger close, wrap the App in a
// bubbletea program, spawn the poller goroutines + push the home
// page (both need prog.Send which doesn't exist until the program
// is constructed), and block on Run.
//
// Every stage that earned a block comment in the pre-boot version
// of this file now lives next to its helper inside
// internal/tui/boot — Build's body is the canonical sequence and
// runTUI is one of two adapter callers (the other being the
// test harness for boot.Build).
func runTUI(cmd *cobra.Command, flags *GlobalFlags) error {
	res, err := boot.Build(cmd.Context(), flags, boot.Deps{
		Version: version,
		Commit:  commit,
		Stderr:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}
	defer res.Close()

	// Deliberately NO tea.WithContext: bubbletea's eventLoop
	// short-circuits on `<-p.ctx.Done()` BEFORE invoking the
	// filter or Update (vendor charm.land/bubbletea/v2 tea.go
	// eventLoop), so on SIGTERM the ctx-cancellation racing with
	// handleSignals' QuitMsg push could win and bypass the
	// page-stack Close cascade entirely. The QuitMsg /
	// InterruptMsg path the filter already covers is sufficient
	// for program-level shutdown. cmd.Context() still propagates
	// to per-page editorCtx / bulkCtx for page-scoped
	// cancellation (silence-form writes, bulk fanout workers,
	// editor updates) — that wiring is in the page factories
	// inside boot and is unaffected by this choice.
	prog := tea.NewProgram(res.App(), tea.WithFilter(boot.QuitFilter(res.App())))

	stop := res.StartPollers(cmd.Context(), prog.Send)
	defer stop()
	res.PushHome(cmd.Context(), prog.Send)

	_, err = prog.Run()
	return err //nolint:wrapcheck // bubbletea's Run error already carries enough context for the operator.
}

// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the a10r command-line interface. The cobra
// root command is built in cmd/root.go; subcommands live alongside
// (cmd/version.go, etc.). This file holds the package entrypoint and
// the goreleaser ldflag-target variables.
package cmd

import (
	"context"
	"os/signal"
	"syscall"
)

// Version metadata is injected at build time via -X ldflags by
// goreleaser. The defaults here let `go build` produce a usable
// binary without ldflags during local development. These are the
// only sanctioned package-level mutable vars in the project;
// everything else uses constructor injection per the "no globals
// beyond sentinels and embeds" rule in CLAUDE.md.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Subcommand groups for `a10r --help` (cobra GroupID). The IDs are
// stable wire identifiers — renaming requires updating each
// subcommand's GroupID assignment below — while the Title is the
// label cobra renders in the help output. New commands must pick
// one of these groups so the help reads coherently.
const (
	groupRead  = "read"
	groupDiag  = "diag"
	groupSetup = "setup"
)

// Execute builds the cobra root command, registers groups +
// subcommands explicitly (no init() side effects), and runs it.
// main() in the repo root simply forwards an error from this
// function.
//
// SIGTERM / SIGINT propagation: the parent context is built via
// signal.NotifyContext so cmd.Context() observes shutdown signals
// across every subcommand. Pages document that their editorCtx /
// bulkCtx parents propagate app shutdown — that contract depends on
// cmd.Context() actually being cancellable; rootCmd.Execute() (no
// ctx) would leave it as context.Background() and the documented
// behaviour would be a lie. Pair with the bubbletea quit-filter in
// cmd/tui.go: together they ensure SIGTERM tears down both the TUI
// page stack and every cobra subcommand cleanly.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var flags GlobalFlags
	rootCmd := newRootCmd(&flags, nil)
	// Group definitions and the completion / help GroupID pins live
	// in newRootCmd so every code path (Execute + tests that build
	// a root directly) sees them.
	rootCmd.AddCommand(
		newVersionCmd(),
		newInfoCmd(&flags),
		newValidateCmd(&flags),
		newDoctorCmd(&flags),
		newInitCmd(&flags),
		newAlertsCmd(&flags),
		newSilencesCmd(&flags),
		newGroupsCmd(&flags),
		newReceiversCmd(&flags),
	)
	return rootCmd.ExecuteContext(ctx)
}

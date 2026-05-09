// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the a10r command-line interface. The cobra
// root command is built in cmd/root.go; subcommands live alongside
// (cmd/version.go, etc.). This file holds the package entrypoint and
// the goreleaser ldflag-target variables.
package cmd

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

// Execute builds the cobra root command, registers every subcommand
// explicitly (no init() side effects), and runs it. main() in the
// repo root simply forwards an error from this function.
func Execute() error {
	var flags GlobalFlags
	rootCmd := newRootCmd(&flags, nil)
	rootCmd.AddCommand(
		newVersionCmd(),
		newInfoCmd(&flags),
		newValidateCmd(&flags),
		newDoctorCmd(&flags),
	)
	return rootCmd.Execute()
}

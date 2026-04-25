// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the a10r command-line interface. The cobra
// root command is wired in cmd/root.go (added in a follow-up commit);
// this file keeps the version-injection variables that the goreleaser
// ldflags target so the build succeeds from commit one.
package cmd

import (
	"fmt"
	"os"
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

// Execute runs the root command. The full cobra wiring lands in a
// follow-up commit; today the binary just prints its build metadata
// so the scaffold is end-to-end runnable.
func Execute() error {
	fmt.Fprintf(os.Stdout, "a10r %s (commit %s, built %s)\n", version, commit, date)
	return nil
}

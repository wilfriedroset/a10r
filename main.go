// SPDX-License-Identifier: Apache-2.0

// Command a10r is a TUI for Prometheus Alertmanager and Grafana Mimir.
package main

import (
	"errors"
	"os"

	"github.com/wilfriedroset/a10r/cmd"
)

func main() {
	err := cmd.Execute()
	if err == nil {
		return
	}
	// cmd.Execute already wrote the failure (a structured envelope or a
	// plain message, per ADR 0045); main only maps it to an exit code.
	// Translate typed exit errors to their declared code; plain errors
	// fall through to ExitRuntimeError so cobra-default failures continue
	// to exit 1. See cmd/exit.go and docs/end-users/exit-codes.md (ADR 0009).
	var ee *cmd.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.Code)
	}
	os.Exit(cmd.ExitRuntimeError)
}

// SPDX-License-Identifier: Apache-2.0

// Command a10r is a TUI for Prometheus Alertmanager and Grafana Mimir.
package main

import (
	"fmt"
	"os"

	"github.com/wilfriedroset/a10r/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

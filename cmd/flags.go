// SPDX-License-Identifier: Apache-2.0

package cmd

import "time"

// GlobalFlags holds the values bound to the cobra root command's
// persistent flags. Per the project's "no globals beyond sentinels
// and embeds" rule, callers construct one per Execute() invocation
// and pass it to subcommands rather than relying on package state.
//
// The flag set mirrors open-question K1 in docs/design/open-questions.md.
// Env var resolution and config-file precedence land alongside the
// internal/config package; this struct is just the CLI-binding shape.
type GlobalFlags struct {
	ConfigDir    string
	LogPath      string
	LogFormat    string
	Debug        bool
	Quiet        bool
	ReadOnly     bool
	Tenant       string
	PollInterval time.Duration
	Theme        string
}

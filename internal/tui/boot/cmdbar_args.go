// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// alertsArgs is the parsed shape of `:alerts` cmdbar arguments. The
// catalogue mirrors the headless `a10r alerts list` flags so a user
// alias `deploy: alerts list --state suppressed` opens the page
// filtered the same way the CLI would have rendered the list.
//
// Positional tokens that aren't recognised flags (e.g. the literal
// `list` in the mirror-the-CLI alias above) are ignored rather than
// rejected: the alias schema's "expanded command line" is meant to
// read like a CLI invocation, and refusing `list` here would force
// operators to learn a TUI-specific dialect.
type alertsArgs struct {
	state  string
	filter string
}

// validAlertStates pins the accepted `--state` values. Matches the
// alerts page's `t`-cycle catalogue (active / suppressed /
// unprocessed) verbatim — keeping the spelling identical means an
// alias that types out one of those words never silently no-ops
// because of a typo we tolerated.
var validAlertStates = []string{
	string(backend.AlertStateActive),
	string(backend.AlertStateSuppressed),
	string(backend.AlertStateUnprocessed),
}

// parseAlertsArgs walks the args slice from a `:alerts ...` prompt
// (or user-alias expansion) and extracts the recognised flags. The
// supported shape is `--flag value` and `--flag=value`; bare
// positional tokens are dropped silently.
//
// Errors quote the offending token so an operator's alias file is
// actionable without re-reading the cmdbar source.
func parseAlertsArgs(args []string) (alertsArgs, error) {
	var out alertsArgs
	for i := 0; i < len(args); i++ {
		tok := args[i]
		key, val, hasEq := parseFlagToken(tok)
		if key == "" {
			continue
		}
		if !hasEq {
			if i+1 >= len(args) {
				return alertsArgs{}, fmt.Errorf("%s: missing value", tok)
			}
			i++
			val = args[i]
		}
		switch key {
		case "state":
			lower := strings.ToLower(strings.TrimSpace(val))
			if !slices.Contains(validAlertStates, lower) {
				return alertsArgs{}, fmt.Errorf("--state %q: must be one of %s",
					val, strings.Join(validAlertStates, ", "))
			}
			out.state = lower
		case "filter":
			out.filter = val
		default:
			return alertsArgs{}, fmt.Errorf("unknown flag --%s (accepted: --state, --filter)", key)
		}
	}
	return out, nil
}

// parseFlagToken splits a CLI-style token into its flag key, the
// embedded value (when present), and a flag indicating whether the
// `=value` shape was used.
func parseFlagToken(tok string) (key, value string, hasEquals bool) {
	if !strings.HasPrefix(tok, "--") {
		return "", "", false
	}
	body := strings.TrimPrefix(tok, "--")
	if body == "" {
		return "", "", false
	}
	if before, after, ok := strings.Cut(body, "="); ok {
		if before == "" {
			return "", "", false
		}
		return before, after, true
	}
	return body, "", false
}

// flashWarnCmd returns a tea.Cmd that emits a Warn-level flash with
// the supplied text. Mirrors the app package's private showFlash —
// duplicated here so the cmdbar wiring layer doesn't need the app
// package to re-export it for one caller.
func flashWarnCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return footer.FlashShowMsg{Level: footer.FlashWarn, Text: text}
	}
}

// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Desktop transports accepted in `tui.notify.desktop`. osc777 carries
// a title and a body, osc9 carries a body only, and both covers a
// terminal whose support the user does not know. See the per-terminal
// table in docs/end-users/configuration.md.
const (
	NotifyDesktopOSC777 = "osc777"
	NotifyDesktopOSC9   = "osc9"
	NotifyDesktopBoth   = "both"
	NotifyDesktopOff    = "off"
)

// NotifyMessagePlaceholder is the argv element a10r replaces with the
// notification text, as one whole argument.
const NotifyMessagePlaceholder = "$MESSAGE"

// DefaultNotifyMinSeverity is the severity floor a notification must
// clear when the user leaves `tui.notify.min_severity` unset. warning
// catches more than critical, which is what an on-caller watching a
// terminal wants from an opt-in feature.
const DefaultNotifyMinSeverity = "warning"

var validNotifyDesktops = []string{NotifyDesktopOSC777, NotifyDesktopOSC9, NotifyDesktopBoth, NotifyDesktopOff}

// validNotifyMinSeverities repeats the names backend.SeverityRank
// weights rather than deriving them: internal/backend imports
// internal/config, so the import cannot run the other way.
// backend.SeverityRank stays the source of truth for the ordering.
// Matching is case-sensitive where SeverityRank lowercases first, so
// `Critical` is rejected here: stricter is the fail-closed direction.
var validNotifyMinSeverities = []string{"critical", "warning", "info"}

// Notify configures the bell and desktop notification a10r raises when
// a poll brings a firing alert the previous poll did not have.
//
// Bell is a pointer because its default is true and Go's zero value is
// false: nil means "the user did not say", which resolves to true.
type Notify struct {
	Enabled     bool     `yaml:"enabled,omitempty"`
	Bell        *bool    `yaml:"bell,omitempty"`
	Desktop     string   `yaml:"desktop,omitempty"`
	MinSeverity string   `yaml:"min_severity,omitempty"`
	Command     []string `yaml:"command,omitempty"`
}

// Validate is fail-closed: a value a10r cannot map to a transport or a
// severity rank halts startup rather than silently notifying nothing.
// An empty Desktop or MinSeverity is legal and resolves through the
// OrDefault accessors.
func (n Notify) Validate() error {
	if n.Desktop != "" && !slices.Contains(validNotifyDesktops, n.Desktop) {
		return fmt.Errorf("tui.notify.desktop must be one of %v (got %q)", validNotifyDesktops, n.Desktop)
	}
	if n.MinSeverity != "" && !slices.Contains(validNotifyMinSeverities, n.MinSeverity) {
		return fmt.Errorf("tui.notify.min_severity must be one of %v (got %q)", validNotifyMinSeverities, n.MinSeverity)
	}
	if len(n.Command) > 0 && strings.TrimSpace(n.Command[0]) == "" {
		return errors.New("tui.notify.command[0]: must not be empty")
	}
	// The head of the argv is the program a10r runs. The placeholder
	// carries an alertname the backend chose, so it must not name it.
	if len(n.Command) > 0 && n.Command[0] == NotifyMessagePlaceholder {
		return fmt.Errorf("tui.notify.command[0]: must not be %q", NotifyMessagePlaceholder)
	}
	return nil
}

// BellOrDefault reports whether the terminal bell rings on a batch.
func (n Notify) BellOrDefault() bool {
	return n.Bell == nil || *n.Bell
}

// DesktopOrDefault resolves the transport. An unset Desktop alongside a
// Command resolves to off so a multiplexer user who routes through a
// subprocess does not also emit escapes the multiplexer swallows.
func (n Notify) DesktopOrDefault() string {
	if n.Desktop != "" {
		return n.Desktop
	}
	if len(n.Command) > 0 {
		return NotifyDesktopOff
	}
	return NotifyDesktopOSC777
}

// MinSeverityOrDefault resolves the severity floor.
func (n Notify) MinSeverityOrDefault() string {
	if n.MinSeverity != "" {
		return n.MinSeverity
	}
	return DefaultNotifyMinSeverity
}

// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strconv"
	"strings"
	"unicode"
)

// windowTitle builds the terminal window / tab title bubbletea emits
// from tea.View.WindowTitle: `a10r: <scope> <crumb>`, plus a
// ` [read-only]` badge when write verbs are disabled. It is empty
// unless the user opts in via `tui.terminal_title`, and bubbletea's
// renderer clears the title on shutdown (cursed_renderer.go's close),
// including the Ctrl+C path, so there is nothing to undo here.
//
// It deliberately ignores filters, marks and poll ticks: a title that
// changes on every keystroke makes window managers flicker.
func (a *App) windowTitle() string {
	if !a.terminalTitle {
		return ""
	}
	title := "a10r: " + sanitizeTitle(scopeLabel(a.scope))
	if p := a.topPage(); p != nil {
		if crumb := sanitizeTitle(p.Crumb()); crumb != "" {
			title += " " + crumb
		}
	}
	if a.session.ReadOnly() {
		title += " [read-only]"
	}
	return title
}

// scopeLabel condenses a comma-joined tenant subset to `<first>+N`,
// because a tab label is a handful of columns wide and the full list
// would push the page crumb off the end.
func scopeLabel(scope string) string {
	if scope == "" {
		return scopeAll
	}
	names := strings.Split(scope, ",")
	if len(names) == 1 {
		return names[0]
	}
	return names[0] + "+" + strconv.Itoa(len(names)-1)
}

// sanitizeTitle drops control characters. A tenant name comes from
// config, and the title is written as an OSC payload, so a crafted
// name must not be able to close the sequence and inject its own.
func sanitizeTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

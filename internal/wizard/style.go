// SPDX-License-Identifier: Apache-2.0

package wizard

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// styler renders the prompter's user-facing chrome (question text,
// brackets, parens, separators) and the highlighted default value.
// The two flavours — color-on / color-off — produce the same layout
// but the off variant emits zero ANSI bytes, so a `bytes.Buffer`
// driver in tests sees the pre-styling format verbatim.
type styler struct {
	color bool
}

func newStyler(color bool) styler { return styler{color: color} }

// String formats a free-form prompt line. defaultValue rendered
// inside `[...]` when non-empty; empty default means no brackets.
// Layout in color-off mode is byte-identical to the pre-styling
// `fmt.Fprintf("%s [%s]: ", q, def)` formatting that prompt_test.go
// asserts on, so existing tests stay green without edits.
func (s styler) String(question, defaultValue string) string {
	if defaultValue == "" {
		return s.chrome(question + ": ")
	}
	return s.chrome(question+" [") + s.defValue(defaultValue) + s.chrome("]: ")
}

// Choice formats a fixed-options prompt line — `(opt1/opt2) [def]`.
// Only the bracketed default is highlighted; the in-parens listing
// stays bold-only so the highlighted value appears exactly once in
// the line.
func (s styler) Choice(question string, choices []string, defaultValue string) string {
	head := question + " (" + strings.Join(choices, "/") + ") ["
	return s.chrome(head) + s.defValue(defaultValue) + s.chrome("]: ")
}

// Bool formats a yes/no prompt line — `[Y/n]` or `[y/N]`. The
// capitalised letter (the default) is highlighted; the slash and
// the lowercase alternative stay bold-only chrome.
func (s styler) Bool(question string, defaultIsYes bool) string {
	if defaultIsYes {
		return s.chrome(question+" [") + s.defValue("Y") + s.chrome("/n]: ")
	}
	return s.chrome(question+" [y/") + s.defValue("N") + s.chrome("]: ")
}

// Invalid formats a re-prompt error line — `  invalid: <body>\n`.
// The body is rendered bold + bright-red so rejected input is
// visually distinct from a fresh prompt. Off-mode emits the same
// `  invalid: ...\n` shape with no escapes.
func (s styler) Invalid(body string) string {
	if !s.color {
		return "  invalid: " + body + "\n"
	}
	return "  invalid: " + invalidStyle.Render(body) + "\n"
}

func (s styler) chrome(text string) string {
	if !s.color {
		return text
	}
	return chromeStyle.Render(text)
}

func (s styler) defValue(text string) string {
	if !s.color {
		return text
	}
	return defaultValueStyle.Render(text)
}

// Pre-built lipgloss styles. Bright-blue (palette 12 → SGR 94) for
// the highlighted default value; bright-red (palette 9 → SGR 91)
// for the invalid-input line body. Both are bold so the styled
// fragments stand out from the surrounding bold-only chrome.
var (
	chromeStyle       = lipgloss.NewStyle().Bold(true)
	defaultValueStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	invalidStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

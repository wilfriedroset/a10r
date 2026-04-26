// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// PromptMode distinguishes the `:` (command) and `/` (filter) modes
// per keybindings.md §Global. The mode is purely cosmetic at this
// layer — both display the same way; what differs is the prefix
// rune the prompt opens with and what the consumer of Submit does
// with the resulting string. Routing belongs in #26.
type PromptMode int

const (
	// PromptCommand opens the `:` command bar.
	PromptCommand PromptMode = iota
	// PromptFilter opens the `/` filter input.
	PromptFilter
)

// prefixGlyph returns the visible glyph for the mode. K9s uses an
// emoji + `>` chevron — same shape here so the visual cue carries
// over for users coming from k9s.
func (m PromptMode) prefixGlyph() string {
	switch m {
	case PromptCommand:
		return "🐶> "
	case PromptFilter:
		return "🐩> "
	}
	return "> "
}

// PromptSubmittedMsg is emitted when the user presses Enter on an
// open prompt. The app shell (#22) routes command-mode submissions
// through the cmdbar resolver and forwards filter-mode submissions
// to the top page so the page can apply the filter.
type PromptSubmittedMsg struct {
	Mode  PromptMode
	Value string
}

// PromptOpenedMsg is forwarded to the top page when a filter
// prompt opens. Pages that want "snapshot filter on /-press,
// restore on Esc" semantics use this hook to capture their pre-
// filter state. Command mode does NOT emit this — the resolver
// owns command-mode submissions end-to-end and the page is not
// involved.
type PromptOpenedMsg struct {
	Mode PromptMode
}

// PromptCancelledMsg is emitted on Esc with no submission. Filter-
// mode cancellations flow through to the top page, which is
// expected to restore the snapshot it took on PromptOpenedMsg.
// Command-mode cancellations terminate at the App.
type PromptCancelledMsg struct {
	Mode PromptMode
}

// Prompt is the bottom-strip input line. v0.1 ships a minimal
// keystroke handler (Backspace / Ctrl+U clear / Enter submit / Esc
// cancel / regular runes append) — adequate for `:command` /
// `/filter` typing without pulling in bubbles/textinput. Once
// suggestions land in #26 this might grow into a wrapper around
// bubbles/textinput; for now the surface is small enough to
// hand-roll.
//
// Prompt knows nothing about routing or alias resolution. It just
// emits PromptSubmittedMsg / PromptCancelledMsg and lets #26 wire
// them.
type Prompt struct {
	open  bool
	mode  PromptMode
	value string
}

// NewPrompt constructs a closed Prompt.
func NewPrompt() Prompt { return Prompt{} }

// Open opens the prompt in the given mode with an empty value.
// Returns the new state; callers should reassign because Prompt is
// a value type.
func (p Prompt) Open(mode PromptMode) Prompt {
	p.open = true
	p.mode = mode
	p.value = ""
	return p
}

// Close closes the prompt without emitting Cancelled (the caller
// already knows what they're doing — typical use is the app shell
// closing the prompt after Submitted).
func (p Prompt) Close() Prompt {
	p.open = false
	p.value = ""
	return p
}

// IsOpen reports whether the prompt is accepting keystrokes.
func (p Prompt) IsOpen() bool { return p.open }

// Mode returns the active mode (only meaningful when IsOpen).
func (p Prompt) Mode() PromptMode { return p.mode }

// Value returns the current input buffer.
func (p Prompt) Value() string { return p.value }

// Update is the keystroke handler. Returns a derivative Prompt and
// an optional tea.Cmd that emits PromptSubmittedMsg / Cancelled.
// Concrete-typed return so callers don't pay for an interface
// assertion. The Prompt does not close itself on Submit — the app
// shell does that after consuming the resulting message.
func (p Prompt) Update(msg tea.Msg) (Prompt, tea.Cmd) {
	if !p.open {
		return p, nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		p.value += paste.Content
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	switch keyMsg.String() {
	case "enter":
		submitted := p.value
		mode := p.mode
		p.open = false
		p.value = ""
		return p, func() tea.Msg { return PromptSubmittedMsg{Mode: mode, Value: submitted} }
	case "esc":
		mode := p.mode
		p.open = false
		p.value = ""
		return p, func() tea.Msg { return PromptCancelledMsg{Mode: mode} }
	case "backspace":
		if p.value != "" {
			r := []rune(p.value)
			p.value = string(r[:len(r)-1])
		}
		return p, nil
	case "ctrl+u":
		p.value = ""
		return p, nil
	}
	// Append the printable character. Prefer Text (the actual entered
	// rune as the terminal reports it, post-IME / shift / dead key);
	// fall back to Code when Text is empty but Code is a printable
	// rune. Function keys, arrows, modifier-only events, and ctrl
	// combos all leave Text empty AND Code outside the printable
	// range, so they naturally drop.
	k := keyMsg.Key()
	if k.Text != "" {
		p.value += k.Text
	} else if k.Mod == 0 && unicode.IsPrint(k.Code) {
		p.value += string(k.Code)
	}
	return p, nil
}

// Render produces the styled prompt line. Returns "" when closed.
// The line is just the body — the App wraps it in a bordered panel
// at render time (see panel.RenderFrame) so the prompt sits above
// the body in the same frame style as the body panel.
func (p Prompt) Render(styles theme.Styles) string {
	if !p.open {
		return ""
	}
	return styles.Prompt.Default.Render(" " + p.mode.prefixGlyph() + p.value + cursorMark)
}

// cursorMark is the visible cursor character. Underscore reads as a
// cursor on most terminal fonts without needing real cursor
// positioning (which bubbletea v2 manages but is overkill for the
// minimal v0.1 prompt).
const cursorMark = "_"

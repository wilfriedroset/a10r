// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// PromptChangedMsg is forwarded to the top page on every prompt
// keystroke (and paste) while open. Pages that filter incrementally
// (k9s-style live filter on `/`) consume it and apply Value
// without committing — a subsequent PromptSubmittedMsg promotes
// the current value to the page's persistent filter, while
// PromptCancelledMsg restores the snapshot the page took on
// PromptOpenedMsg. Command-mode keystrokes also emit this so
// future per-page command suggestions can react if needed; pages
// that don't care just ignore it.
type PromptChangedMsg struct {
	Mode  PromptMode
	Value string
}

// Prompt is the bottom-strip input line. Hand-rolled keystroke
// handler — Backspace / Ctrl+U clear / Enter submit / Esc cancel /
// regular runes append, plus Tab / Ctrl+F to accept a ghost-text
// completion suggestion in command mode. Adequate for `:command` /
// `/filter` typing without pulling in bubbles/textinput.
//
// Prompt knows nothing about routing or alias resolution. The
// `suggester` dep is the single seam between the alias registry
// and the prompt's render; everything else (PromptSubmittedMsg /
// Cancelled / Changed) flows through messages the App routes.
type Prompt struct {
	open  bool
	mode  PromptMode
	value string

	// suggester returns the alphabetically-first registered alias
	// that has the buffer as a prefix, or "" for empty / nomatch /
	// exact-match. nil is a graceful no-op so wizard / headless
	// flows can construct a Prompt without wiring the cmdbar.
	// Invoked only in command mode — filter mode has no completion
	// source in this iteration.
	suggester func(string) string
	// suggestion caches the last suggester output, recomputed on
	// every text-changing branch in Update so Render is purely a
	// function of cached state.
	suggestion string
}

// NewPrompt constructs a closed Prompt with the given suggester.
// Pass nil to disable ghost-text completion (filter-only flows,
// tests that don't care about suggestions).
func NewPrompt(suggester func(string) string) Prompt {
	return Prompt{suggester: suggester}
}

// Open opens the prompt in the given mode with an empty value.
// Returns the new state; callers should reassign because Prompt is
// a value type.
func (p Prompt) Open(mode PromptMode) Prompt {
	p.open = true
	p.mode = mode
	p.value = ""
	p.suggestion = ""
	return p
}

// Close closes the prompt without emitting Cancelled (the caller
// already knows what they're doing — typical use is the app shell
// closing the prompt after Submitted).
func (p Prompt) Close() Prompt {
	p.open = false
	p.value = ""
	p.suggestion = ""
	return p
}

// IsOpen reports whether the prompt is accepting keystrokes.
func (p Prompt) IsOpen() bool { return p.open }

// Mode returns the active mode (only meaningful when IsOpen).
func (p Prompt) Mode() PromptMode { return p.mode }

// Value returns the current input buffer.
func (p Prompt) Value() string { return p.value }

// Suggestion returns the current ghost-text completion candidate,
// or "" when there is no ghost (filter mode, no suggester wired,
// or the buffer has no matching alias).
func (p Prompt) Suggestion() string { return p.suggestion }

// Update is the keystroke handler. Returns a derivative Prompt and
// an optional tea.Cmd that emits PromptSubmittedMsg / Cancelled, or
// PromptChangedMsg whenever the buffer mutates so live-filter
// pages can react per-keystroke. Concrete-typed return so callers
// don't pay for an interface assertion. The Prompt does not close
// itself on Submit — the app shell does that after consuming the
// resulting message.
func (p Prompt) Update(msg tea.Msg) (Prompt, tea.Cmd) {
	if !p.open {
		return p, nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		p.value += paste.Content
		p.recomputeSuggestion()
		return p, p.changedCmd()
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
		p.suggestion = ""
		return p, func() tea.Msg { return PromptSubmittedMsg{Mode: mode, Value: submitted} }
	case "esc":
		mode := p.mode
		p.open = false
		p.value = ""
		p.suggestion = ""
		return p, func() tea.Msg { return PromptCancelledMsg{Mode: mode} }
	case "backspace":
		if p.value != "" {
			r := []rune(p.value)
			p.value = string(r[:len(r)-1])
			p.recomputeSuggestion()
			return p, p.changedCmd()
		}
		return p, nil
	case "ctrl+u":
		if p.value == "" {
			return p, nil
		}
		p.value = ""
		p.recomputeSuggestion()
		return p, p.changedCmd()
	case "tab", "ctrl+f":
		// Accept the ghost-text completion. The mode guard is
		// belt-and-braces with recomputeSuggestion (which clears
		// the cache outside command mode) — keeps Tab consumption
		// safe even if a future code path mutates p.mode without
		// going through Open/Close.
		if p.mode != PromptCommand || p.suggestion == "" {
			return p, nil
		}
		p.value = p.suggestion + " "
		p.recomputeSuggestion()
		return p, p.changedCmd()
	}
	// Append the printable character. Prefer Text (the actual entered
	// rune as the terminal reports it, post-IME / shift / dead key);
	// fall back to Code when Text is empty but Code is a printable
	// rune. Function keys, arrows, modifier-only events, and ctrl
	// combos all leave Text empty AND Code outside the printable
	// range, so they naturally drop.
	prev := p.value
	k := keyMsg.Key()
	if k.Text != "" {
		p.value += k.Text
	} else if k.Mod == 0 && unicode.IsPrint(k.Code) {
		p.value += string(k.Code)
	}
	if p.value == prev {
		return p, nil
	}
	p.recomputeSuggestion()
	return p, p.changedCmd()
}

// recomputeSuggestion refreshes the ghost-text candidate against
// the current buffer. Filter mode and a nil suggester both clear
// it — the seam exists only for command-mode alias completion.
//
// Defensive: a suggester that returns a string which doesn't have
// the buffer as a prefix would render as garbled overlay (the
// trim in Render would no-op and the full suggestion would glue
// after the cursor). Drop the result rather than render it.
func (p *Prompt) recomputeSuggestion() {
	if p.mode != PromptCommand || p.suggester == nil {
		p.suggestion = ""
		return
	}
	s := p.suggester(p.value)
	if s != "" && !strings.HasPrefix(s, p.value) {
		s = ""
	}
	p.suggestion = s
}

// changedCmd builds the per-keystroke broadcast Cmd. Captures the
// mode + value at call time so the Cmd is safe to fire later even
// if the prompt closes / mutates in the interim.
func (p Prompt) changedCmd() tea.Cmd {
	mode, value := p.mode, p.value
	return func() tea.Msg { return PromptChangedMsg{Mode: mode, Value: value} }
}

// Render produces the styled prompt line. Returns "" when closed.
// The line is just the body — the App wraps it in a bordered panel
// at render time (see panel.RenderFrame) so the prompt sits above
// the body in the same frame style as the body panel.
//
// The typed segment is bolded (Prompt.Default fg) and the ghost is
// plain weight (Prompt.Suggestion fg). The bold-vs-plain boundary
// is the cue that separates "what the user typed" from "what the
// ghost would complete to" — k9s renders the same way.
//
// Foreground-only on purpose: the surrounding panel.RenderFrame is
// unstyled and the rest of the chrome lets the terminal default bg
// show through, so painting the prompt's palette bg behind the
// glyph + buffer would render as a coloured stripe inside the
// otherwise transparent frame. The ghost segment obeys the same
// rule — fg only via styles.Prompt.Suggestion.
func (p Prompt) Render(styles theme.Styles) string {
	if !p.open {
		return ""
	}
	main := lipgloss.NewStyle().
		Foreground(styles.Prompt.Default.GetForeground()).
		Bold(true)
	out := main.Render(" " + p.mode.prefixGlyph() + p.value)
	if p.suggestion != "" {
		ghost := lipgloss.NewStyle().Foreground(styles.Prompt.Suggestion.GetForeground())
		out += ghost.Render(strings.TrimPrefix(p.suggestion, p.value))
	}
	return out
}

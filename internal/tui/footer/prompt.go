// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"strings"
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

	// history is the recent-submissions ring backing the
	// Up/Down/Tab/Shift-Tab cycle keys. Attached at Open() time by
	// the App because the right ring depends on the active page
	// (silences `/` walks a different class than `/` on every
	// other page) and the prompt mode (`:` always uses cmd-history).
	// nil disables cycling — the prompt stays usable in headless /
	// wizard flows that build a prompt without an XDG home.
	history *History
}

// NewPrompt constructs a closed Prompt with the given suggester.
// Pass nil to disable ghost-text completion (filter-only flows,
// tests that don't care about suggestions).
func NewPrompt(suggester func(string) string) Prompt {
	return Prompt{suggester: suggester}
}

// Open opens the prompt in the given mode with an empty value.
// Returns the new state; callers should reassign because Prompt is
// a value type. The previously-attached history (if any) stays
// detached — call OpenWithHistory to enable Up/Down/Tab cycling.
func (p Prompt) Open(mode PromptMode) Prompt {
	return p.OpenWithHistory(mode, nil)
}

// OpenWithHistory opens the prompt and attaches a History ring so
// Up/Down (and Tab when there's no ghost-text suggestion) cycle
// through prior submissions for the relevant matcher class. Pass
// nil to open without cycling. The supplied ring is reset so a
// fresh prompt session starts uncycled regardless of where the
// previous user left off.
func (p Prompt) OpenWithHistory(mode PromptMode, history *History) Prompt {
	p.open = true
	p.mode = mode
	p.value = ""
	p.suggestion = ""
	p.history = history
	p.history.Reset()
	return p
}

// Close closes the prompt without emitting Cancelled (the caller
// already knows what they're doing — typical use is the app shell
// closing the prompt after Submitted).
func (p Prompt) Close() Prompt {
	p.open = false
	p.value = ""
	p.suggestion = ""
	p.history.Reset()
	p.history = nil
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
//
// The body is a dispatch table over keyMsg.String(); each case
// delegates to a small named helper so the routing stays scannable
// and the per-key logic lives next to a comment that explains it.
func (p Prompt) Update(msg tea.Msg) (Prompt, tea.Cmd) {
	if !p.open {
		return p, nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		p.value += paste.Content
		p = p.recomputeSuggestion()
		return p, p.changedCmd()
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	switch keyMsg.String() {
	case "enter":
		return p.submit()
	case "esc":
		return p.cancel()
	case "backspace":
		return p.deleteRune()
	case "ctrl+u":
		return p.clearBuffer()
	case "tab", "ctrl+f":
		return p.acceptOrCyclePrev()
	case "up":
		// Up always means "older" — works even when a ghost
		// completion is active (Tab would accept it; Up still
		// walks history).
		return p.cyclePrev()
	case "shift+tab", "down":
		// Inverse pair to Tab / Up. Shift+Tab steps newer mid-
		// cycle so the user can back out without committing.
		return p.cycleNext()
	}
	return p.appendRune(keyMsg)
}

// submit closes the prompt, stashes the entry in history, and
// emits PromptSubmittedMsg with the value captured at submit time.
func (p Prompt) submit() (Prompt, tea.Cmd) {
	submitted, mode := p.value, p.mode
	p.history.Append(submitted)
	p.open = false
	p.value = ""
	p.suggestion = ""
	p.history = nil
	return p, func() tea.Msg { return PromptSubmittedMsg{Mode: mode, Value: submitted} }
}

// cancel closes the prompt without recording the buffer and emits
// PromptCancelledMsg so live-filter pages can restore their
// pre-prompt snapshot.
func (p Prompt) cancel() (Prompt, tea.Cmd) {
	mode := p.mode
	p.history.Reset()
	p.open = false
	p.value = ""
	p.suggestion = ""
	p.history = nil
	return p, func() tea.Msg { return PromptCancelledMsg{Mode: mode} }
}

// deleteRune trims the trailing rune from the buffer; empty
// buffer is a quiet no-op so backspace at the start does not emit
// a spurious Changed.
func (p Prompt) deleteRune() (Prompt, tea.Cmd) {
	if p.value == "" {
		return p, nil
	}
	r := []rune(p.value)
	p.value = string(r[:len(r)-1])
	p = p.recomputeSuggestion()
	return p, p.changedCmd()
}

// clearBuffer empties the value in one keystroke (Ctrl+U).
// Already-empty is a no-op for the same reason as deleteRune.
func (p Prompt) clearBuffer() (Prompt, tea.Cmd) {
	if p.value == "" {
		return p, nil
	}
	p.value = ""
	p = p.recomputeSuggestion()
	return p, p.changedCmd()
}

// acceptOrCyclePrev handles the Tab / Ctrl+F dual binding: accept
// the ghost-text completion when one is showing, otherwise step
// backward through history. The completion path runs first because
// the user typed a prefix and the ghost is the immediate visible
// affordance — surprising them by cycling instead would shadow the
// more obvious action. Ctrl+F mirrors Tab for users who can't
// easily reach Tab; same precedence.
func (p Prompt) acceptOrCyclePrev() (Prompt, tea.Cmd) {
	if p.mode == PromptCommand && p.suggestion != "" {
		p.value = p.suggestion + " "
		p = p.recomputeSuggestion()
		return p, p.changedCmd()
	}
	return p.cyclePrev()
}

// appendRune handles the printable-character fallback when no
// named binding consumed the key. Prefer Text (the actual entered
// rune as the terminal reports it, post-IME / shift / dead key);
// fall back to Code when Text is empty but Code is a printable
// rune. Function keys, arrows, modifier-only events, and ctrl
// combos all leave Text empty AND Code outside the printable
// range, so they naturally drop.
func (p Prompt) appendRune(keyMsg tea.KeyMsg) (Prompt, tea.Cmd) {
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
	p = p.recomputeSuggestion()
	return p, p.changedCmd()
}

// cyclePrev steps the history cursor toward older entries and
// replaces the buffer with the entry under the new cursor. Returns
// the unmodified prompt + nil when the prompt has no history wired
// or the cursor is already on the oldest entry — both are quiet
// no-ops, not errors. The first Prev in a cycle session stashes the
// current buffer so cycle-past-newest can restore it later.
func (p Prompt) cyclePrev() (Prompt, tea.Cmd) {
	if p.history == nil {
		return p, nil
	}
	v, ok := p.history.Prev(p.value)
	if !ok {
		return p, nil
	}
	if v == p.value {
		// No-op: the entry under the new cursor matches what's
		// already in the buffer. Don't broadcast a Changed for a
		// non-mutation — pages would otherwise re-filter for
		// nothing.
		return p, nil
	}
	p.value = v
	p = p.recomputeSuggestion()
	return p, p.changedCmd()
}

// cycleNext steps toward newer entries; returns the stashed draft
// and ends the cycle session when the cursor crosses the newest
// entry. Same no-mutation guard as cyclePrev so a draft equal to
// the entry just before "present" doesn't double-broadcast.
func (p Prompt) cycleNext() (Prompt, tea.Cmd) {
	if p.history == nil {
		return p, nil
	}
	v, ok := p.history.Next()
	if !ok {
		return p, nil
	}
	if v == p.value {
		return p, nil
	}
	p.value = v
	p = p.recomputeSuggestion()
	return p, p.changedCmd()
}

// recomputeSuggestion refreshes the ghost-text candidate against
// the current buffer and returns the updated Prompt. Filter mode
// and a nil suggester both clear it — the seam exists only for
// command-mode alias completion. The value-receiver shape matches
// the rest of Prompt's API so callers stay consistent
// (`p = p.recomputeSuggestion()`).
//
// Defensive: a suggester that returns a string which doesn't have
// the buffer as a prefix would render as garbled overlay (the
// trim in Render would no-op and the full suggestion would glue
// after the cursor). Drop the result rather than render it.
func (p Prompt) recomputeSuggestion() Prompt {
	if p.mode != PromptCommand || p.suggester == nil {
		p.suggestion = ""
		return p
	}
	s := p.suggester(p.value)
	if s != "" && !strings.HasPrefix(s, p.value) {
		s = ""
	}
	p.suggestion = s
	return p
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
func (p Prompt) Render(styles *theme.Styles) string {
	if !p.open {
		return ""
	}
	out := styles.Prompt.DefaultFgBold.Render(" " + p.mode.prefixGlyph() + p.value)
	if p.suggestion != "" {
		// Suggestion is already FgOnly at theme-load (see compilePrompt),
		// so reading it directly is the fg-only ghost the chrome wants.
		out += styles.Prompt.Suggestion.Render(strings.TrimPrefix(p.suggestion, p.value))
	}
	return out
}

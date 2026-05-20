// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// FlashLevel is the colour band a flash renders in; the theme owns
// the per-level fg/bg colours so a skin re-paints all four levels.
type FlashLevel int

const (
	FlashSuccess FlashLevel = iota
	FlashInfo
	FlashWarn
	FlashError
)

// DefaultFlashTTL is the auto-clear timeout for transient flash
// messages. k9s uses ~1.5 s; a 4 s ceiling lets the user actually
// read longer error messages without losing them to a fast tick.
const DefaultFlashTTL = 4 * time.Second

// FlashShowMsg is the input the app shell sends to make a flash
// appear. The Flash component schedules its own auto-clear tick.
type FlashShowMsg struct {
	Level FlashLevel
	Text  string
	// TTL overrides DefaultFlashTTL when non-zero. Use sparingly —
	// consistency across the TUI matters more than per-call tuning.
	TTL time.Duration
}

// ShowFlash returns the tea.Cmd that surfaces a flash with the given
// level + text. Every page that wanted to flash previously inlined
// the same `return func() tea.Msg { return FlashShowMsg{...} }`
// closure; the wording "ShowFlash(FlashWarn, …)" reads as the intent
// at the call site.
func ShowFlash(level FlashLevel, text string) tea.Cmd {
	return func() tea.Msg {
		return FlashShowMsg{Level: level, Text: text}
	}
}

// flashClearMsg is the internal tick that clears an active flash.
// id distinguishes overlapping flashes: a fresh FlashShowMsg
// supersedes a pending clear, so the older clear's id no longer
// matches and is dropped. uint64 so a long-running session with
// frequent toasts cannot wrap and resurrect a stale id collision.
type flashClearMsg struct {
	id uint64
}

// Flash is the bottom-strip transient-message line. Auto-clears
// via tea.Tick after the configured TTL. The component doesn't
// choose colours from the level — it hands off to the theme — so
// a future skin can re-paint the four flash levels without
// touching this file.
type Flash struct {
	level FlashLevel
	text  string
	id    uint64
}

// NewFlash constructs a closed Flash.
func NewFlash() Flash { return Flash{} }

// Update routes a message into the Flash. Returns the new state
// plus the tea.Tick command that schedules the auto-clear when a
// Show message arrives. Concrete-typed return so callers don't pay
// for an interface assertion.
func (f Flash) Update(msg tea.Msg) (Flash, tea.Cmd) {
	switch m := msg.(type) {
	case FlashShowMsg:
		ttl := m.TTL
		if ttl <= 0 {
			ttl = DefaultFlashTTL
		}
		f.id++
		f.level = m.Level
		f.text = m.Text
		id := f.id
		return f, tea.Tick(ttl, func(time.Time) tea.Msg {
			return flashClearMsg{id: id}
		})
	case flashClearMsg:
		// Only clear if the tick matches the current generation.
		// A newer FlashShowMsg may have superseded this clear.
		if m.id == f.id {
			f.text = ""
		}
		return f, nil
	}
	return f, nil
}

// Text returns the active flash text. Used by callers that need
// the unstyled message (e.g. accessibility / log lines).
func (f Flash) Text() string { return f.text }

// IsActive reports whether the flash currently has visible text.
func (f Flash) IsActive() bool { return f.text != "" }

// Owns reports whether msg is a Flash-domain message (the public
// FlashShowMsg or the internal auto-clear tick). The App uses this
// to route unrecognised messages without enumerating the unexported
// internal type — keeping flashClearMsg an implementation detail
// while still giving the app shell explicit, auditable routing.
func (Flash) Owns(msg tea.Msg) bool {
	switch msg.(type) {
	case FlashShowMsg, flashClearMsg:
		return true
	default:
		return false
	}
}

// Render produces the styled flash line. Returns "" when no flash
// is active so the app shell can collapse the strip.
func (f Flash) Render(styles *theme.Styles) string {
	if f.text == "" {
		return ""
	}
	style := flashStyle(f.level, styles)
	return style.Render(f.text)
}

// flashStyle returns the lipgloss style for the given level.
// FlashInfo is the default fall-through.
func flashStyle(level FlashLevel, styles *theme.Styles) lipgloss.Style {
	switch level {
	case FlashSuccess:
		return styles.Flash.Success
	case FlashWarn:
		return styles.Flash.Warn
	case FlashError:
		return styles.Flash.Error
	default:
		return styles.Flash.Info
	}
}

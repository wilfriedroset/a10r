// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/help"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// DefaultHintBarInterval is the rotation cadence used when the user
// enables `tui.tips` without naming an explicit `tui.tips_interval`.
// Eight seconds is long enough to read a one-line tip without
// glancing twice and short enough that a session of any length still
// cycles through the curated set. Picked by hand; no contract on the
// exact value — users in a hurry shorten it via config.
const DefaultHintBarInterval = 8 * time.Second

// HintBar is the optional rotating tip strip rendered above the
// crumbs / flash strips when the user opts in via `tui.tips: true`.
// The component is value-typed and follows the same Update / Render
// / Owns shape as Flash so the App composes it as a field, not as a
// nested tea.Model.
//
// The strip defaults OFF to honour the "no scouted features without
// explicit go" project rule — a zero-value HintBar has enabled=false,
// fires no tick, and renders the empty string. The wiring layer
// constructs an enabled HintBar only when the loaded config sets
// `tui.tips: true`.
//
// Rotation is driven by a tea.Tick scheduled from Start (called by
// the App's Init when enabled) and re-armed on every tick. Each tick
// advances the rotating index by one and schedules the next tick;
// the index wraps modulo len(tips) so the strip never runs out.
//
// An empty `tips` slice short-circuits identically to the disabled
// path: no tick, no render. This guards against a future curated-set
// mistake where Tips() returns nothing.
type HintBar struct {
	tips     []help.Tip
	interval time.Duration
	enabled  bool

	// idx is the rotation cursor. Always in [0, len(tips)) when
	// enabled and len(tips) > 0; ignored otherwise.
	idx int

	// generation tags every scheduled tick so a stale tick from a
	// superseded timer round drops silently on arrival. v0.0.1 only
	// increments it on construction (always zero), but the same
	// idiom Flash uses leaves room for a future hot-reload to
	// invalidate pending ticks without changing the wire shape.
	generation uint64
}

// hintBarTickMsg is the internal tick that advances the rotation.
// Carries the generation it was scheduled under so a stale tick from
// a superseded timer round drops silently. Unexported because
// callers route via HintBar.Owns; the App never names this type.
type hintBarTickMsg struct {
	generation uint64
}

// HintBarOptions bundles the constructor inputs. Pulled out into a
// struct so a future knob (e.g. theme override, custom tip source
// for testing without monkey-patching help.Tips) lands as a new
// field without breaking call sites.
type HintBarOptions struct {
	// Enabled flips the entire component on. False (the zero value)
	// keeps the bar dormant: no tick fires, Render returns "".
	Enabled bool

	// Interval is the rotation cadence. Zero or negative falls back
	// to DefaultHintBarInterval so a partial config (`tui.tips:
	// true` alone) still works.
	Interval time.Duration

	// Tips is the curated catalogue to rotate through. Nil falls
	// back to help.Tips() — the production default — so the wiring
	// layer doesn't need to import the help package just to build
	// the bar.
	Tips []help.Tip
}

// NewHintBar constructs a HintBar from opts. Always returns a
// well-formed value: a disabled bar when Enabled is false OR Tips
// resolves to an empty slice, an enabled bar otherwise. The caller
// kicks rotation off via Start (called from App.Init) so the tea
// program owns the timer lifecycle.
func NewHintBar(opts HintBarOptions) HintBar {
	tips := opts.Tips
	if tips == nil {
		tips = help.Tips()
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultHintBarInterval
	}
	return HintBar{
		tips:     tips,
		interval: interval,
		enabled:  opts.Enabled && len(tips) > 0,
	}
}

// Enabled reports whether the bar is opted-in AND has a non-empty
// curated set. Tests use this; the App reads it to decide whether to
// schedule the initial tick.
func (h HintBar) Enabled() bool { return h.enabled }

// Interval returns the configured rotation cadence. Always a
// positive duration once the constructor has run, even on a
// disabled bar (so a future Start after a hot-reload has a value to
// schedule against).
func (h HintBar) Interval() time.Duration { return h.interval }

// Start returns the tea.Cmd that schedules the first rotation tick
// when the bar is enabled. Disabled / empty-tips bars return nil so
// the program never schedules a no-op tick. The App calls Start once
// from Init when the configured options enabled the bar.
func (h HintBar) Start() tea.Cmd {
	if !h.enabled {
		return nil
	}
	return h.tickCmd()
}

// Update routes a message into the bar. Returns the (possibly
// advanced) state plus the next-tick Cmd when the message was a
// hintBarTickMsg under the current generation. Disabled bars return
// (h, nil) for every input — the OFF short-circuit the project
// memory mandates.
func (h HintBar) Update(msg tea.Msg) (HintBar, tea.Cmd) {
	if !h.enabled {
		return h, nil
	}
	tick, ok := msg.(hintBarTickMsg)
	if !ok {
		return h, nil
	}
	if tick.generation != h.generation {
		// Stale tick from a superseded generation. Drop without
		// advancing the cursor and without rescheduling — the new
		// generation owns the active timer.
		return h, nil
	}
	h.idx = (h.idx + 1) % len(h.tips)
	return h, h.tickCmd()
}

// Owns reports whether msg is a HintBar-domain message. Mirrors
// Flash.Owns so the App can route messages without enumerating the
// unexported tick type. Disabled bars still own their tick type so
// a stale tick stamped before any future disable terminates inside
// Update rather than escaping to the page stack.
func (HintBar) Owns(msg tea.Msg) bool {
	_, ok := msg.(hintBarTickMsg)
	return ok
}

// Current returns the tip the bar is currently displaying. Returns
// the zero Tip when the bar is disabled or the catalogue is empty —
// callers should branch on Enabled before reading.
func (h HintBar) Current() help.Tip {
	if !h.enabled {
		return help.Tip{}
	}
	return h.tips[h.idx]
}

// Render produces the styled one-line strip. Returns "" when the
// bar is disabled so the App's footer joiner collapses the row and
// the body fills the freed line. The chrome stays foreground-only
// (terminal-default bg) per the project memory on TUI chrome — a
// painted bg here would create a visible stripe inside the
// otherwise unstyled frame. The key chip routes through
// help.ChipText so the help overlay and the hint bar share one
// ligature-safe rule.
func (h HintBar) Render(styles *theme.Styles) string {
	if !h.enabled {
		return ""
	}
	tip := h.tips[h.idx]
	chip := styles.Hint.HelpKey.Render(help.ChipText(tip.Key))
	body := styles.Hint.DefaultFg.Render(tip.Text)
	return chip + "  " + body
}

// tickCmd schedules the next rotation tick under the current
// generation. The closure captures the generation by value so a
// future generation bump renders pending ticks stale on arrival
// without needing to track or cancel the live timer.
func (h HintBar) tickCmd() tea.Cmd {
	gen := h.generation
	return tea.Tick(h.interval, func(time.Time) tea.Msg {
		return hintBarTickMsg{generation: gen}
	})
}

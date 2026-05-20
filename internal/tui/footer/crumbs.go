// SPDX-License-Identifier: Apache-2.0

// Package footer renders the bottom strip of the TUI: crumbs,
// prompt, flash. Each subcomponent is a value-typed bubble: Update
// returns its concrete type (not tea.Model) so callers don't pay
// for a type assertion and the receiver type is unambiguous in
// reviews. The app shell (#22) composes them as fields and forwards
// messages explicitly; there's no aggregating tea.Model in this
// package, by design.
package footer

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// crumbSeparator is the gap between adjacent crumb pills. k9s
// uses a single space between `<…>` chips, no chevron — the pill
// shape is the visual cue, an extra glyph between them is noise.
const crumbSeparator = " "

// Crumbs renders the page-stack breadcrumb strip in the footer.
// Pages don't push directly here — the app shell owns the page
// stack and rebuilds Crumbs from it on every push/pop. Crumbs.Set
// rebuilds in one shot; Push/Pop are convenience helpers for code
// that prefers an incremental API.
type Crumbs struct {
	entries []string
}

// NewCrumbs constructs an empty Crumbs.
func NewCrumbs() Crumbs { return Crumbs{} }

// Render produces the styled crumb strip given the theme. Each
// entry is wrapped in `<…>` and rendered bold as a pill
// (foreground + background). The k9s convention — see the
// `logs.png` reference in the look-and-feel doc — gives every
// crumb its own pill, with the top-of-stack crumb wearing the
// brighter accent colour and the older crumbs wearing a related
// but distinct colour. We achieve that by:
//
//   - using theme.Crumbs.Default's fg+bg for inactive crumbs
//     (the schema's `crumbs.fg` / `crumbs.bg`);
//   - swapping the bg to the accent (theme.Crumbs.Active's fg
//     colour, repurposed as a background) for the top-of-stack
//     crumb. The crumbs.active palette colour is therefore the
//     "current page" pill background, with the same fg as the
//     default pill so the contrast stays consistent.
//
// Empty crumbs render as the empty string so the app shell can
// omit the strip entirely.
func (c Crumbs) Render(styles *theme.Styles) string {
	if len(c.entries) == 0 {
		return ""
	}
	defaultStyle := styles.Crumbs.DefaultBold
	activeStyle := styles.Crumbs.ActivePill
	parts := make([]string, len(c.entries))
	last := len(c.entries) - 1
	for i, e := range c.entries {
		style := defaultStyle
		if i == last {
			style = activeStyle
		}
		parts[i] = style.Render("<" + e + ">")
	}
	// Separator stays unstyled (foreground default of the
	// surrounding text) so it reads as a gap between pills rather
	// than another pill.
	return strings.Join(parts, crumbSeparator)
}

// Set replaces the breadcrumb stack wholesale. The most common
// shape after the app shell processes a push or pop.
func (c Crumbs) Set(entries []string) Crumbs {
	cp := make([]string, len(entries))
	copy(cp, entries)
	c.entries = cp
	return c
}

// Push adds an entry on top of the stack. Always allocates a fresh
// backing array so a caller holding the pre-Push value continues to
// see its original entries even if the post-Push value is mutated
// later — matching Set's value-type semantics.
func (c Crumbs) Push(label string) Crumbs {
	cp := make([]string, len(c.entries)+1)
	copy(cp, c.entries)
	cp[len(c.entries)] = label
	c.entries = cp
	return c
}

// Pop removes the top entry. Returns the unchanged Crumbs if the
// stack is already empty — over-popping is a no-op so the page-
// stack semantics survive a stray Esc at the root.
func (c Crumbs) Pop() Crumbs {
	if len(c.entries) == 0 {
		return c
	}
	cp := make([]string, len(c.entries)-1)
	copy(cp, c.entries[:len(c.entries)-1])
	c.entries = cp
	return c
}

// Len returns the current breadcrumb depth.
func (c Crumbs) Len() int { return len(c.entries) }

// Top returns the active (top-of-stack) entry, or "" when empty.
func (c Crumbs) Top() string {
	if len(c.entries) == 0 {
		return ""
	}
	return c.entries[len(c.entries)-1]
}

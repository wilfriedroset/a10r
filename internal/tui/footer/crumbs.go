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

// crumbSeparator visually separates breadcrumbs (e.g. `alerts > detail > silence`).
const crumbSeparator = " › "

// Crumbs renders the page-stack breadcrumb strip per the k9s audit
// §2 layout. Pages don't push directly here — the app shell (#22)
// owns the page stack and rebuilds Crumbs from it on every push/
// pop. Crumbs.Set rebuilds in one shot; Push/Pop are convenience
// helpers for code that prefers an incremental API.
type Crumbs struct {
	entries []string
}

// NewCrumbs constructs an empty Crumbs.
func NewCrumbs() Crumbs { return Crumbs{} }

// Render produces the styled crumb strip given the theme. The last
// entry (top-of-stack) gets the active highlight; everything else
// uses the default foreground. Empty crumbs render as the empty
// string so the app shell can omit the strip entirely.
func (c Crumbs) Render(styles theme.Styles) string {
	if len(c.entries) == 0 {
		return ""
	}
	parts := make([]string, len(c.entries))
	last := len(c.entries) - 1
	for i, e := range c.entries {
		if i == last {
			parts[i] = styles.Crumbs.Active.Render(e)
		} else {
			parts[i] = styles.Crumbs.Default.Render(e)
		}
	}
	sep := styles.Crumbs.Default.Render(crumbSeparator)
	return strings.Join(parts, sep)
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

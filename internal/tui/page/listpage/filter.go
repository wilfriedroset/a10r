// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
)

// SetFilter classifies s under the page's Grammar and makes it the
// page filter. It is the only writer, so the buffer and the grammar
// that owns it cannot drift apart. A buffer the grammar refuses is
// recorded in FilterErr and reported as false, and the previous
// classification stays in place, so the rows freeze on the last good
// filter while the user types. A buffer that compiles clears the
// error. The caller owns the Recompute call, because only it knows
// whether the rows need rebuilding.
func (b *Base) SetFilter(s string) bool {
	c, err := filterexpr.Compile(s, b.Grammar)
	b.FilterErr = err
	if err != nil {
		return false
	}
	b.filter = c
	return true
}

// FilterBuffer returns the text the user typed.
func (b *Base) FilterBuffer() string { return b.filter.Buffer() }

// FilterMode returns the title's mode tag without its brackets, empty
// when the buffer needs none.
func (b *Base) FilterMode() string { return b.filter.ModeLabel() }

// FilterMatch reports whether r survives the active filter.
func (b *Base) FilterMatch(r filterexpr.Row) bool { return b.filter.Match(r) }

// FilterMatchAll reports whether every row survives, which lets a
// page hand its input slice straight to the view.
func (b *Base) FilterMatchAll() bool { return b.filter.MatchAll() }

// FilterSpans returns the reporter the row renderer hands to
// format.Highlighter, or nil when the active filter paints nothing.
// Pages call it once per frame and run it over the visible window
// only, never over the whole list.
//
// Which buffers paint nothing is decided once, when the buffer is
// set. See filterexpr.Compiled.Spans for the four cases.
func (b *Base) FilterSpans() func(string) [][2]int { return b.filter.Spans() }

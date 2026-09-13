// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

// FilterSpans returns the reporter the row renderer hands to
// format.Highlighter, or nil when the active filter paints nothing.
// Pages call it once per frame and run it over the visible window
// only, never over the whole list.
//
// Three buffers paint nothing. An empty one, because no filter is
// set. A bare sigil, because it has no text to look for. A label
// selector on a page that reads selectors, because it matches on
// label structure rather than on the rendered text — FilterValidate
// is the same signal the prompt uses to tell the two grammars apart.
//
// A regex that does not compile keeps the substring fallback
// NewMatcher returns, so the highlight tracks the rows the recompute
// kept. The chrome reports the error separately, via FilterErr.
func (b *Base) FilterSpans() func(string) [][2]int {
	if b.Filter == "" {
		return nil
	}
	if b.FilterValidate != nil {
		if _, err := matcher.LabelPredicate(b.Filter); err == nil {
			return nil
		}
	}
	if _, body := footer.TrimSearchPrefix(b.Filter); body == "" {
		// A bare sigil keeps every row and reports no span, so the page
		// would scan every visible cell for nothing.
		return nil
	}
	m, _ := footer.NewMatcher(b.Filter)
	return m.MatchSpans
}

// SPDX-License-Identifier: Apache-2.0

package listpage

import (
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/filter"
	"github.com/wilfriedroset/a10r/internal/tui/filterexpr"
)

// FilterSpans returns the reporter the row renderer hands to
// format.Highlighter, or nil when the active filter paints nothing.
// Pages call it once per frame and run it over the visible window
// only, never over the whole list.
//
// Four buffers paint nothing. An empty one, because no filter is
// set. A bare sigil, because it has no text to look for. A label
// selector on a page whose Grammar reads selectors, because it
// matches on label structure rather than on the rendered text. An
// expression on a page whose Grammar reads expressions, because its
// matching characters are spread across terms the reporter cannot
// attribute, and painting the whole buffer as one needle would
// highlight the wrong cells.
//
// A regex that does not compile keeps the substring fallback
// NewMatcher returns, so the highlight tracks the rows the recompute
// kept. The chrome reports the error separately, via FilterErr.
func (b *Base) FilterSpans() func(string) [][2]int {
	if b.Filter == "" {
		return nil
	}
	if b.Grammar.Expressions {
		if expr, _ := filterexpr.CompileExpr(b.Filter); expr != nil {
			return nil
		}
	}
	if b.Grammar.Selectors {
		if _, err := matcher.LabelPredicate(b.Filter); err == nil {
			return nil
		}
	}
	if _, body := filter.TrimSearchPrefix(b.Filter); body == "" {
		// A bare sigil keeps every row and reports no span, so the page
		// would scan every visible cell for nothing.
		return nil
	}
	m, _ := filter.NewMatcher(b.Filter)
	return m.MatchSpans
}

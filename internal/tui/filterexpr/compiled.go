// SPDX-License-Identifier: Apache-2.0

package filterexpr

import (
	"errors"
	"fmt"

	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/filter"
)

// Grammar declares which term languages a page reads. The zero
// value is text only.
type Grammar struct {
	Selectors   bool
	Expressions bool
}

// AlertGrammar is the grammar the alerts list and group detail
// read: label selectors and boolean expressions.
var AlertGrammar = Grammar{Selectors: true, Expressions: true}

// Compiled is one classified buffer: which grammar owns it, and
// everything that follows from that. The zero value matches
// everything and paints nothing.
//
// Compile it once, where the buffer becomes the page filter, and
// read it everywhere else. The predicate the recompute runs, the
// spans the renderer paints and the mode label the title shows are
// then three reads of one value and cannot disagree.
type Compiled struct {
	buffer string
	expr   *Expr
	labels func(map[string]string) bool
	text   filter.Matcher
	mode   filter.SearchMode
	// paints is true when a text term owns the whole buffer and has
	// a body to look for, which is the only case the renderer can
	// attribute to cells.
	paints bool
}

// Compile classifies buffer under g. The expression parser gets
// first refusal on a page that reads expressions, a buffer it does
// not own is judged as a selector on a page that reads selectors,
// and the rest is the five-mode text path.
//
// The error prefix names the language that refused the buffer, so
// the title tag, the Enter flash and the `:alerts --filter`
// rejection all read the same. A rejected buffer still comes back
// as the text term it would have been, so a caller that applies it
// keeps the rows live while the user types. A refusal skips the
// rungs below it, which for `expr:` drops the selector rung the
// old ladder would still have tried: the two readings part only
// on a buffer that is both a broken expression and a parseable
// selector, and the prompt refuses that buffer either way.
func Compile(buffer string, g Grammar) (Compiled, error) {
	text, regexErr := filter.NewMatcher(buffer)
	mode, body := filter.TrimSearchPrefix(buffer)
	c := Compiled{
		buffer: buffer,
		text:   text,
		mode:   mode,
		paints: body != "",
	}
	if g.Expressions {
		expr, err := CompileExpr(buffer)
		if err != nil {
			return c, fmt.Errorf("expr: %s", filter.RegexErrText(err))
		}
		if expr != nil {
			c.expr, c.mode, c.paints = expr, filter.SearchExpression, false
			return c, nil
		}
	}
	if g.Selectors {
		pred, err := matcher.LabelPredicate(buffer)
		switch {
		case err == nil:
			c.labels, c.paints = pred, false
			return c, nil
		case errors.Is(err, matcher.ErrNotMatcher):
			// Not a selector at all, so the text path below owns it.
		default:
			return c, fmt.Errorf("matcher: %s", filter.RegexErrText(err))
		}
	}
	if regexErr != nil {
		return c, fmt.Errorf("regex: %s", filter.RegexErrText(regexErr))
	}
	return c, nil
}

// Buffer returns the text the user typed.
func (c Compiled) Buffer() string { return c.buffer }

// MatchAll reports whether every row survives, which lets a page
// hand its input slice straight to the view. Only the empty buffer
// skips the loop: every other buffer is a classification the
// recompute has to run row by row, a bare sigil included.
func (c Compiled) MatchAll() bool { return c.buffer == "" }

// IsExpr reports whether the boolean expression grammar owns the
// buffer. A page reads it to skip work only an expression can ask
// for, such as the alerts page's group-level pre-aggregation.
func (c Compiled) IsExpr() bool { return c.expr != nil }

// Match reports whether r survives the filter, whichever grammar
// won.
func (c Compiled) Match(r Row) bool {
	switch {
	case c.MatchAll():
		return true
	case c.expr != nil:
		return c.expr.Match(r)
	case c.labels != nil:
		return c.labels(r.Labels)
	default:
		return c.text.Match(r.Text)
	}
}

// Spans returns the reporter the row renderer hands to
// format.Highlighter, or nil when the buffer paints nothing.
//
// Four buffers paint nothing. An empty one, because no filter is
// set. A bare sigil, because it has no text to look for. A label
// selector, because it matches on label structure rather than on
// the rendered text. An expression, because its matching characters
// are spread across terms the reporter cannot attribute, and
// painting the whole buffer as one needle would highlight the wrong
// cells.
func (c Compiled) Spans() func(lowered string) [][2]int {
	if !c.paints {
		return nil
	}
	return c.text.MatchSpans
}

// ModeLabel returns the title's mode tag without its brackets:
// empty for an empty or substring buffer, "expr" when the
// expression grammar owns it, and the detected five-mode label
// otherwise. The app keeps the styling and the brackets.
func (c Compiled) ModeLabel() string {
	if c.mode == filter.SearchSubstring {
		return ""
	}
	return c.mode.String()
}

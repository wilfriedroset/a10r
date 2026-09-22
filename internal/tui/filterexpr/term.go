// SPDX-License-Identifier: Apache-2.0

package filterexpr

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/filter"
	"github.com/wilfriedroset/a10r/internal/tui/timerender"
)

const (
	keyCount = "count"
	keyAge   = "age"
	keyState = "state"
)

// compOps is the comparison-operator table. The two-character forms
// come first so a tie at the same index resolves in their favour --
// the rule matcher.ops already applies, kept identical here so a
// term splits the same way in both places.
var compOps = []string{"!=", ">=", "<=", "=~", "!~", "=", ">", "<"}

// parts is one term split on its operator. rawKey keeps the user's
// casing for the error message; key is lower-cased for dispatch.
type parts struct {
	key    string
	rawKey string
	op     string
	val    string
}

func compileTerm(raw string) (node, error) {
	t := strings.TrimSpace(raw)
	p, ok := splitTerm(t)
	if !ok {
		return textTerm(t)
	}
	switch p.key {
	case keyCount:
		return countTerm(p)
	case keyAge:
		return ageTerm(p)
	case keyState:
		return stateTerm(p)
	}
	if isOrderOp(p.op) {
		return nil, fmt.Errorf("unknown key %q", p.rawKey)
	}
	return labelTerm(t)
}

// splitTerm finds the leftmost comparison operator outside quotes at
// an index past zero. A leading operator reads as no operator at
// all, matching matcher.ParseOne, so such a term falls through to
// text search instead of yielding an empty key. The value loses its
// outer pair of double quotes here so every typed key reads a quoted
// value the same way. labelTerm re-parses the raw term and so never
// sees this value.
func splitTerm(t string) (parts, bool) {
	best := -1
	var bestOp string
	for _, op := range compOps {
		i := indexOutsideQuotes(t, op)
		if i <= 0 || (best != -1 && i >= best) {
			continue
		}
		best, bestOp = i, op
	}
	if best == -1 {
		return parts{}, false
	}
	rawKey := strings.TrimSpace(t[:best])
	return parts{
		key:    strings.ToLower(rawKey),
		rawKey: rawKey,
		op:     bestOp,
		val:    stripQuotes(strings.TrimSpace(t[best+len(bestOp):])),
	}, true
}

func indexOutsideQuotes(s, sub string) int {
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch {
		case inQuote && s[i] == '\\' && i+1 < len(s):
			i++
		case s[i] == '"':
			inQuote = !inQuote
		case !inQuote && strings.HasPrefix(s[i:], sub):
			return i
		}
	}
	return -1
}

func countTerm(p parts) (node, error) {
	if err := checkTypedOp(p); err != nil {
		return nil, err
	}
	want, err := strconv.Atoi(p.val)
	if err != nil {
		return nil, fmt.Errorf("bad count %q", p.val)
	}
	return gated(countAvail, func(r Row) bool {
		return orderedMatch(p.op, r.Count, want)
	}), nil
}

func ageTerm(p parts) (node, error) {
	if err := checkTypedOp(p); err != nil {
		return nil, err
	}
	want, err := timerender.Parse(p.val)
	if err != nil {
		return nil, fmt.Errorf("bad duration %q", p.val)
	}
	return gated(ageAvail, func(r Row) bool {
		return orderedMatch(p.op, r.Now.Sub(r.Start), want)
	}), nil
}

func stateTerm(p parts) (node, error) {
	if err := checkTypedOp(p); err != nil {
		return nil, err
	}
	want := strings.ToLower(p.val)
	return gated(instanceAvail, func(r Row) bool {
		return orderedMatch(p.op, r.State, want)
	}), nil
}

func labelTerm(t string) (node, error) {
	pred, err := matcher.LabelPredicate(t)
	if errors.Is(err, matcher.ErrNotMatcher) {
		return textTerm(t)
	}
	if err != nil {
		// Bare on purpose: a selector whose regex will not compile is
		// one the user meant and got wrong, so the prompt reports the
		// compile error rather than silently searching for the text.
		//nolint:wrapcheck // see above
		return nil, err
	}
	return gated(instanceAvail, func(r Row) bool { return pred(r.Labels) }), nil
}

// textTerm compiles a free-text term. A `/.../` wrapper forces
// regex mode for a pattern the filter package's meta-count
// auto-detection would leave as a substring, and the lexer treats
// every byte between the slashes as ordinary, so the pattern can
// carry a delimiter. A label value needs quoting for the same effect.
func textTerm(t string) (node, error) {
	if len(t) >= 2 && t[0] == '/' && t[len(t)-1] == '/' {
		re, err := regexp.Compile("(?i)" + t[1:len(t)-1])
		if err != nil {
			//nolint:wrapcheck // the syntax error is what the prompt renders
			return nil, err
		}
		return gated(instanceAvail, func(r Row) bool { return re.MatchString(r.Text) }), nil
	}
	m, err := filter.NewMatcher(t)
	if err != nil {
		//nolint:wrapcheck // filter owns the message the prompt renders
		return nil, err
	}
	return gated(instanceAvail, func(r Row) bool { return m.Match(r.Text) }), nil
}

// isTypedTerm reports whether raw is a typed comparison, the one
// term shape that alone hands a buffer to the expression parser.
func isTypedTerm(raw string) bool {
	p, ok := splitTerm(strings.TrimSpace(raw))
	return ok && isTypedKey(p.key) && !isRegexOp(p.op)
}

func isTypedKey(k string) bool {
	return k == keyCount || k == keyAge || k == keyState
}

func isRegexOp(op string) bool { return op == "=~" || op == "!~" }

func isOrderOp(op string) bool {
	return op == ">" || op == ">=" || op == "<" || op == "<="
}

func checkTypedOp(p parts) error {
	if isRegexOp(p.op) {
		return fmt.Errorf("bad operator %q for key %q", p.op, p.key)
	}
	return nil
}

func orderedMatch[T cmp.Ordered](op string, got, want T) bool {
	switch op {
	case "=":
		return got == want
	case "!=":
		return got != want
	case ">":
		return got > want
	case ">=":
		return got >= want
	case "<":
		return got < want
	case "<=":
		return got <= want
	}
	return false
}

func stripQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

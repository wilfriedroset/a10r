// SPDX-License-Identifier: Apache-2.0

// Package filterexpr parses the `/` filter prompt's boolean
// expression grammar -- `&&`, `,` and juxtaposition for AND, `||`
// for OR, `!` for NOT, parentheses for grouping -- and evaluates it
// against one row.
//
// Evaluation is three-valued. A term over a value the row does not
// carry is unknown, not false, so neither the term nor its negation
// matches. Only a definite true matches.
package filterexpr

import (
	"strings"
	"time"
)

// Avail says whether a typed value is usable in the current pass.
type Avail uint8

const (
	// Missing means the value does not exist for this row. The term
	// and its negation both fail (Kleene unknown).
	Missing Avail = iota
	// Present means compare against the value.
	Present
)

// Row is the projection of one table row an expression evaluates
// against. The caller supplies each value together with its
// availability so the same expression can run over a partially
// loaded view without a missing value reading as a mismatch.
type Row struct {
	Now        time.Time
	Labels     map[string]string
	Text       string // lower-cased label+annotation composite
	State      string
	Instance   Avail // governs label, text and state terms
	Count      int
	CountAvail Avail
	Start      time.Time
	AgeAvail   Avail
}

// Expr is a compiled filter expression. Construct it with Parse;
// the zero value is not usable.
type Expr struct{ root node }

// Match reports whether r satisfies the expression.
func (e *Expr) Match(r Row) bool { return e.root(r) == triTrue }

// Compile returns the expression to run for s, or a nil Expr and a
// nil error when the five-mode path owns the buffer. An `&&` or `,`
// chain alone stays on the old path so today's buffers keep their
// meaning, and a leading `\` forces literal mode over the whole
// buffer.
//
//nolint:nilnil // the nil pair is the documented answer above
func Compile(s string) (*Expr, error) {
	if !isExpr(s) {
		return nil, nil
	}
	return Parse(s)
}

// isExpr scans s for the tokens that hand a buffer to the expression
// parser. A `(` needs an explicit `&&` or `,` beside it, because a
// lone paren is also a regex metacharacter and `(web|api)` has to
// stay the alternation the user meant rather than become a group
// around a substring.
func isExpr(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || t[0] == '\\' {
		return false
	}
	var sawParen, sawAnd bool
	for _, tok := range lex(t) {
		switch tok.kind {
		case tokOr, tokNot:
			return true
		case tokLParen:
			sawParen = true
		case tokAnd:
			sawAnd = true
		case tokTerm:
			if isTypedTerm(tok.text) {
				return true
			}
		case tokRParen, tokEOF:
		}
	}
	return sawParen && sawAnd
}

// tri is the Kleene truth value evaluation runs on.
type tri uint8

const (
	triFalse tri = iota
	triTrue
	triUnknown
)

func triOf(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

// node is a compiled sub-expression.
type node func(Row) tri

func andNode(kids []node) node {
	return func(r Row) tri {
		out := triTrue
		for _, k := range kids {
			switch k(r) {
			case triFalse:
				return triFalse
			case triUnknown:
				out = triUnknown
			case triTrue:
			}
		}
		return out
	}
}

func orNode(kids []node) node {
	return func(r Row) tri {
		out := triFalse
		for _, k := range kids {
			switch k(r) {
			case triTrue:
				return triTrue
			case triUnknown:
				out = triUnknown
			case triFalse:
			}
		}
		return out
	}
}

func notNode(k node) node {
	return func(r Row) tri {
		got := k(r)
		if got == triUnknown {
			return triUnknown
		}
		return triOf(got == triFalse)
	}
}

// gated applies a term's availability rule before its predicate.
func gated(avail func(Row) Avail, pred func(Row) bool) node {
	return func(r Row) tri {
		switch avail(r) {
		case Present:
			return triOf(pred(r))
		case Missing:
		}
		return triUnknown
	}
}

func instanceAvail(r Row) Avail { return r.Instance }

func countAvail(r Row) Avail { return r.CountAvail }

// ageAvail downgrades a present-but-zero start time to Missing: an
// alert without a StartsAt has no age, so comparing it against the
// epoch would answer every age term with a meaningless true.
func ageAvail(r Row) Avail {
	if r.AgeAvail == Present && r.Start.IsZero() {
		return Missing
	}
	return r.AgeAvail
}

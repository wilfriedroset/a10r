// SPDX-License-Identifier: Apache-2.0

// Package filterexpr parses the `/` filter prompt's boolean
// expression grammar -- `&&`, `,` and juxtaposition for AND, `||`
// for OR, `!` for NOT, parentheses for grouping -- and evaluates it
// against one row.
//
// Evaluation is three-valued. A term over a value the row does not
// carry is unknown, not false, so neither the term nor its negation
// matches; a term another pass owns (a server-side selector, say)
// reports Deferred and reads as true here. Only a definite true
// matches.
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
	// Deferred means another pass owns the term, so it is true here.
	Deferred
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

// IsExpr reports whether s should go to Parse rather than the
// single-matcher path. An `&&` or `,` chain alone stays on the old
// path so today's buffers keep their meaning, and a leading `\`
// forces literal mode over the whole buffer. IsExpr only lexes, so
// it neither panics nor reports a parse error.
func IsExpr(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || t[0] == '\\' {
		return false
	}
	for _, tok := range lex(t) {
		switch tok.kind {
		case tokOr, tokNot, tokLParen:
			return true
		case tokTerm:
			if isTypedTerm(tok.text) {
				return true
			}
		case tokAnd, tokRParen, tokEOF:
		}
	}
	return false
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
		case Deferred:
			return triTrue
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

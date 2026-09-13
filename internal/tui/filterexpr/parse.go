// SPDX-License-Identifier: Apache-2.0

package filterexpr

import (
	"errors"
	"fmt"
	"strings"
)

// maxDepth caps how far `(` and `!` may nest so a pathological
// buffer cannot recurse the parser into a stack overflow.
const maxDepth = 32

// Parse compiles s into an Expr. Precedence is `!` tightest, then
// AND, then OR; two terms separated only by whitespace are ANDed.
func Parse(s string) (*Expr, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("empty filter")
	}
	p := &parser{toks: lex(s)}
	root, err := p.parseOr(0)
	if err != nil {
		return nil, err
	}
	if p.peek() != tokEOF {
		// parseOr consumes every token kind but `)`, so anything
		// left over is a close paren with no group to close.
		return nil, errors.New("unexpected )")
	}
	return &Expr{root: root}, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() tokKind {
	if p.pos >= len(p.toks) {
		return tokEOF
	}
	return p.toks[p.pos].kind
}

func (p *parser) next() token {
	tok := p.toks[p.pos]
	p.pos++
	return tok
}

// startsUnary reports whether the next token can begin a unary,
// which is also the test for an implicit AND.
func (p *parser) startsUnary() bool {
	k := p.peek()
	return k == tokTerm || k == tokNot || k == tokLParen
}

func (p *parser) parseOr(depth int) (node, error) {
	first, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	kids := []node{first}
	for p.peek() == tokOr {
		p.next()
		if !p.startsUnary() {
			return nil, missingTermErr(tokOr)
		}
		n, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return orNode(kids), nil
}

func (p *parser) parseAnd(depth int) (node, error) {
	first, err := p.parseUnary(depth)
	if err != nil {
		return nil, err
	}
	kids := []node{first}
	for p.peek() == tokAnd || p.startsUnary() {
		if p.peek() == tokAnd {
			p.next()
			if !p.startsUnary() {
				return nil, missingTermErr(tokAnd)
			}
		}
		n, err := p.parseUnary(depth)
		if err != nil {
			return nil, err
		}
		kids = append(kids, n)
	}
	if len(kids) == 1 {
		return first, nil
	}
	return andNode(kids), nil
}

func (p *parser) parseUnary(depth int) (node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nesting deeper than %d", maxDepth)
	}
	switch p.peek() {
	case tokNot:
		return p.parseNot(depth)
	case tokLParen:
		return p.parseGroup(depth)
	case tokTerm:
		return compileTerm(p.next().text)
	case tokAnd, tokOr:
		return nil, missingTermErr(p.peek())
	case tokRParen:
		return nil, errors.New("unexpected )")
	case tokEOF:
	}
	// EOF only reaches here from inside a group: every other caller
	// checks startsUnary before recursing.
	return nil, errors.New("unbalanced (")
}

func (p *parser) parseNot(depth int) (node, error) {
	p.next()
	if !p.startsUnary() {
		// The trailing `!` is the operator the message names, not
		// sentence punctuation.
		//nolint:revive,staticcheck // see above
		return nil, errors.New("empty term after !")
	}
	n, err := p.parseUnary(depth + 1)
	if err != nil {
		return nil, err
	}
	return notNode(n), nil
}

func (p *parser) parseGroup(depth int) (node, error) {
	p.next()
	if p.peek() == tokRParen {
		return nil, errors.New("empty group")
	}
	inner, err := p.parseOr(depth + 1)
	if err != nil {
		return nil, err
	}
	if p.peek() != tokRParen {
		return nil, errors.New("unbalanced (")
	}
	p.next()
	return inner, nil
}

func missingTermErr(k tokKind) error {
	if k == tokOr {
		return errors.New("missing term after ||")
	}
	return errors.New("missing term after &&")
}

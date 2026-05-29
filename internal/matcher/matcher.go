// SPDX-License-Identifier: Apache-2.0

// Package matcher parses Prometheus-style label matchers (the
// `name<op>value` form used in --matcher flags, silence forms, and
// alert label selectors). The four supported operators are `=`,
// `!=`, `=~`, `!~`. The package returns backend.Matcher directly so
// callers do not project through their own value type.
package matcher

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// ErrMissingOperator is returned when no `=`, `!=`, `=~`, or `!~`
// operator is found at a position strictly greater than zero. A
// leading-match position (e.g. `=oops`) is treated as missing-
// operator rather than empty-name so a stray operator at column 1
// surfaces the same way as an entirely operator-less input.
var ErrMissingOperator = errors.New("missing operator (=, !=, =~, !~)")

// ErrIncompleteMatcher is returned when an operator is found but
// the name side or the value side trims to empty (e.g. `severity=`,
// `  =critical`). The single sentinel covers both halves because the
// two failure modes share the user-facing remediation: complete the
// `name<op>value` shape.
var ErrIncompleteMatcher = errors.New("matcher must be name<op>value")

type opDef struct {
	s       string
	isRegex bool
	isEqual bool
}

// ops is the operator table searched left-to-right; the two-char
// entries come first so that a tie at the same string index (e.g.
// `foo=~bar` — `=~` at 3 vs `=` at 3) resolves in favour of the
// two-char operator. The loop below only updates bestIdx on a
// strictly-smaller index, never a tie, so source order is the
// tie-breaker. Round-trip semantics depend on this: a value that
// itself contains an operator (e.g. `foo=a!=b`) must split on the
// leftmost `=`, not on the later `!=`.
var ops = []opDef{
	{s: "!~", isRegex: true, isEqual: false},
	{s: "=~", isRegex: true, isEqual: true},
	{s: "!=", isRegex: false, isEqual: false},
	{s: "=", isRegex: false, isEqual: true},
}

// ParseOne splits a single matcher string on its leftmost operator
// and returns the resulting backend.Matcher. Whitespace around the
// name and value is trimmed; a single layer of balanced double
// quotes around the value is stripped so a shell-quoted Prom
// invocation (`severity="critical"`) and a bare one
// (`severity=critical`) reach the same matcher.
func ParseOne(s string) (backend.Matcher, error) {
	bestIdx := -1
	var bestOp opDef
	for _, o := range ops {
		idx := strings.Index(s, o.s)
		if idx <= 0 {
			continue
		}
		if bestIdx == -1 || idx < bestIdx {
			bestIdx = idx
			bestOp = o
		}
	}
	if bestIdx == -1 {
		return backend.Matcher{}, ErrMissingOperator
	}
	name := strings.TrimSpace(s[:bestIdx])
	value := strings.TrimSpace(s[bestIdx+len(bestOp.s):])
	if name == "" || value == "" {
		return backend.Matcher{}, ErrIncompleteMatcher
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	return backend.Matcher{
		Name:    name,
		Value:   value,
		IsRegex: bestOp.isRegex,
		IsEqual: bestOp.isEqual,
	}, nil
}

// LabelPredicate parses s as a single label matcher and returns a
// predicate over a label set plus ok=true, for filtering a list by a
// Prometheus-style selector (`cluster_id=99`, `cluster_id=~9.*`,
// `cluster_id!=99`, `cluster_id!~prod-.*`).
//
// It returns (nil, false) — telling the caller to fall back to its
// substring / fuzzy / regex text search — when s carries the footer
// prompt's text-mode sigils (a leading `~` for fuzzy or `\` for
// literal), does not parse as a matcher (no operator, e.g. a bare
// word), or carries an uncompilable regex (so a half-typed pattern
// degrades to text search rather than dropping every row).
//
// The `=~` / `!~` regex is compiled ONCE here and fully anchored
// (`^(?:…)$`) so it matches whole label values — the same semantics
// Alertmanager applies server-side. A label absent from the set reads
// as the empty value, so `name!=v` matches series without the label
// (standard Prometheus behaviour).
func LabelPredicate(s string) (func(labels map[string]string) bool, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	switch s[0] {
	case '~', '\\':
		return nil, false
	}
	m, err := ParseOne(s)
	if err != nil {
		return nil, false
	}
	if !m.IsRegex {
		return func(labels map[string]string) bool {
			return (labels[m.Name] == m.Value) == m.IsEqual
		}, true
	}
	re, err := regexp.Compile("^(?:" + m.Value + ")$")
	if err != nil {
		return nil, false
	}
	return func(labels map[string]string) bool {
		return re.MatchString(labels[m.Name]) == m.IsEqual
	}, true
}

// Parse walks one-matcher-per-line input, returning the parsed
// matchers in source order. Blank lines and lines that trim to
// empty are skipped; the first parse error short-circuits with a
// `line N: <reason>` wrap so the caller can point at the offending
// row in a multi-line text area.
func Parse(in string) ([]backend.Matcher, error) {
	out := make([]backend.Matcher, 0)
	for i, raw := range strings.Split(in, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		m, err := ParseOne(line)
		if err != nil {
			return nil, errLineWrap(i+1, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// Op renders just the operator symbol for the given matcher's
// IsRegex / IsEqual flags. Inverse of the operator detection in
// ParseOne: the four (IsRegex, IsEqual) combinations map back to
// the four operator strings in the same order ops lists them.
func Op(m backend.Matcher) string {
	switch {
	case m.IsRegex && m.IsEqual:
		return "=~"
	case m.IsRegex && !m.IsEqual:
		return "!~"
	case !m.IsRegex && m.IsEqual:
		return "="
	default:
		return "!="
	}
}

func errLineWrap(line int, err error) error {
	return errors.New("line " + strconv.Itoa(line) + ": " + err.Error())
}

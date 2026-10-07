// SPDX-License-Identifier: Apache-2.0

package filterexpr

type tokKind uint8

const (
	tokEOF tokKind = iota
	tokTerm
	tokAnd
	tokOr
	tokNot
	tokLParen
	tokRParen
)

type token struct {
	kind tokKind
	text string
}

// lex splits s into tokens. Every operator is ASCII, so the scan is
// byte-wise and multi-byte runes ride through inside terms
// untouched. Whitespace between terms is dropped rather than turned
// into a token: synthesising the implicit AND is the parser's job.
func lex(s string) []token {
	var toks []token
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case isSpace(c):
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen})
			i++
		case c == ',':
			toks = append(toks, token{kind: tokAnd})
			i++
		case c == '&' && i+1 < len(s) && s[i+1] == '&':
			toks = append(toks, token{kind: tokAnd})
			i += 2
		case c == '|' && i+1 < len(s) && s[i+1] == '|':
			toks = append(toks, token{kind: tokOr})
			i += 2
		case c == '!':
			toks = append(toks, token{kind: tokNot})
			i++
		default:
			text, next := scanTerm(s, i)
			toks = append(toks, token{kind: tokTerm, text: text})
			i = next
		}
	}
	return toks
}

// scanTerm reads one term starting at i, stopping at the first
// unquoted delimiter. `!` is ordinary here, so `severity!=info` is
// one term while a leading `!` has already been taken as NOT by the
// caller. A `\` escapes the next byte inside quotes only, which
// leaves a leading `\` sigil intact in the term text. A leading
// `/.../` pattern is stepped over first, so a regex can carry a
// delimiter that a label value would have to be quoted to keep.
func scanTerm(s string, i int) (text string, next int) {
	start := i
	if s[i] == '/' {
		i = skipSlashPattern(s, i)
	}
	inQuote := false
	for ; i < len(s); i++ {
		switch {
		case inQuote && s[i] == '\\' && i+1 < len(s):
			i++
		case s[i] == '"':
			inQuote = !inQuote
		case !inQuote && endsTerm(s, i):
			return s[start:i], i
		}
	}
	return s[start:], i
}

// skipSlashPattern returns the index just past the closing `/` of a
// `/.../` pattern opening at i, or i unchanged when nothing closes
// it. Every byte between the slashes is ordinary: a `"` is a legal
// regex byte, so quoting does not apply, and `\` escapes one byte so
// a pattern can carry an escaped `/`.
func skipSlashPattern(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '/' {
			return j + 1
		}
	}
	return i
}

// endsTerm reports whether the byte at i closes a term. `&&` and
// `||` are the only two-byte delimiters and both are a doubled
// byte, so one test covers them.
func endsTerm(s string, i int) bool {
	c := s[i]
	if isSpace(c) || c == '(' || c == ')' || c == ',' {
		return true
	}
	return (c == '&' || c == '|') && i+1 < len(s) && s[i+1] == c
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

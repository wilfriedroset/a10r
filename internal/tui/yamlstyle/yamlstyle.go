// SPDX-License-Identifier: Apache-2.0

// Package yamlstyle applies the skin's YAML.Key / .Punct / .Value
// foreground roles to a YAML body. Best-effort line-level: a `key:`
// prefix on a line tints the key; trailing value gets the value
// style. Lines that don't match the simple pattern (lists, multi-
// line values, comments) render with the default body fg so
// structure stays legible without the renderer needing a full YAML
// AST walker.
//
// Shared by every read-only YAML viewer (tenant-config, silence
// detail, alert detail) so a skin tweak lights up consistently
// across pages.
package yamlstyle

import (
	"strings"

	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// Body styles every line in body. Empty input returns "".
func Body(body string, styles *theme.Styles) string {
	if body == "" {
		return ""
	}
	out := make([]string, 0, strings.Count(body, "\n")+1)
	for line := range strings.SplitSeq(body, "\n") {
		out = append(out, Line(line, styles))
	}
	return strings.Join(out, "\n")
}

// Line applies skin colours to one YAML line. Pure so it's easy to
// test in isolation. Comment-only lines pass through unstyled (the
// text after `#` is human prose, not a key/value pair); lines
// without a `:` short-circuit too. Lines whose pre-`:` segment
// can't be a real YAML key (contains brackets, equals, etc.) also
// pass through — that catches wrap continuations and \n-split
// value segments like "LABELS = map[__name__:up]" that contain a
// `:` purely incidentally.
func Line(line string, styles *theme.Styles) string {
	if isCommentLine(line) {
		return line
	}
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return line
	}
	prefixEnd := 0
	for prefixEnd < idx && (line[prefixEnd] == ' ' || line[prefixEnd] == '-') {
		prefixEnd++
	}
	indent := line[:prefixEnd]
	key := line[prefixEnd:idx]
	if !looksLikeYAMLKey(key) {
		return line
	}
	rest := line[idx:]
	punctEnd := 1
	if punctEnd < len(rest) && rest[punctEnd] == ' ' {
		punctEnd++
	}
	punct := rest[:punctEnd]
	value := rest[punctEnd:]
	styled := indent + styles.YAML.Key.Render(key) + styles.YAML.Punct.Render(punct)
	if value != "" {
		styled += styles.YAML.Value.Render(value)
	}
	return styled
}

// looksLikeYAMLKey rejects key candidates whose shape disqualifies
// them from being a real YAML key — so a Prometheus annotation
// continuation like `      LABELS = map[__name__:up]` is rendered
// unstyled instead of having "LABELS = map[__name__" tinted as a
// key. Allows letters, digits, `_`, `-`, `.`, and spaces (so
// human-readable keys the alert detail builds — `Generator URL`,
// `silenced by` — keep their styling).
func looksLikeYAMLKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ' ':
		default:
			return false
		}
	}
	return true
}

// isCommentLine reports whether the line's first non-whitespace
// (or non-list-marker) character is `#`. Skips leading spaces and
// `-` so list-element comments ("- # foo") and indented comments
// pass through too.
func isCommentLine(line string) bool {
	trimmed := strings.TrimLeft(line, " -")
	return strings.HasPrefix(trimmed, "#")
}

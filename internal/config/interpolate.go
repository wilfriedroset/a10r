// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"regexp"
)

// envVarPattern matches `${NAME}` and `${NAME:-default}`. NAME must
// follow shell rules: ASCII letter or underscore start, alnum or
// underscore thereafter. Captured groups:
//
//  1. NAME.
//  2. default value — nil when the `:-` clause is omitted, empty
//     bytes when `:-` is given with no value (e.g. `${FOO:-}`).
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// interpolateBytes resolves `${NAME}` and `${NAME:-default}`
// occurrences in the input against the supplied getenv.
//
// Semantics intentionally diverge from POSIX in one direction:
// `${NAME}` requires the env var to be **non-empty**, not merely
// set, before substituting. An exported-but-empty value (`export
// NAME=`) errors the same as unset. The motivation is config-file
// ergonomics — a bearer token of "" is broken either way, so it is
// safer to surface the misconfiguration than to silently emit an
// empty value. Users who want a literally empty value should write
// the literal in YAML or use the `${NAME:-}` fallback form, which
// substitutes empty whenever NAME is unset OR empty.
//
// The default-value class accepts any character except `}` and the
// substituted value is passed through verbatim. Newlines or YAML
// metacharacters in env values flow through to the YAML stream and
// can break the surrounding document — keep defaults single-line and
// avoid env values like "a:\nb" inside scalar fields.
//
// All occurrences are walked in a single pass; the first unresolved
// variable wins the error so the user fixes them top-down.
func interpolateBytes(in []byte, getenv func(string) string) ([]byte, error) {
	var firstErr error
	out := envVarPattern.ReplaceAllFunc(in, func(match []byte) []byte {
		groups := envVarPattern.FindSubmatch(match)
		name := string(groups[1])
		hasDefault := groups[2] != nil

		if value := getenv(name); value != "" {
			return []byte(value)
		}
		if hasDefault {
			return groups[2]
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("env var %q is not set and no default given", name)
		}
		return match
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

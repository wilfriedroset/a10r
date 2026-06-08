// SPDX-License-Identifier: Apache-2.0

package output

import "slices"

// EnvOutput is the environment variable that sets a default output
// format when no --output flag is given. It sits one layer below the
// flag in the ADR 0027 precedence chain (flag → A10R_OUTPUT → agent
// detection → TTY-derived default).
const EnvOutput = "A10R_OUTPUT"

// ResolveAgentAware picks the effective output format, layering the
// agent affordances of ADR 0045 beneath the explicit flag:
//
//  1. a non-empty format (the --output flag) always wins;
//  2. else A10R_OUTPUT, but only when its value is one of allowed —
//     an ambient env default is best-effort and must not break a
//     command a bare invocation would have run (so e.g. "table" on a
//     get, whose allowed set is json/yaml, falls through rather than
//     erroring);
//  3. else json when an AI coding agent is detected;
//  4. else the TTY-derived default (ttyDefault on a terminal,
//     pipeDefault otherwise).
//
// Write verbs pass "" for both defaults so their non-agent default
// stays the tab-separated lines mode. getenv is injected for testing
// and tolerated nil (treated as an empty environment).
func ResolveAgentAware(
	format Format,
	getenv func(string) string,
	tty bool,
	allowed []Format,
	ttyDefault, pipeDefault Format,
) Format {
	if format != "" {
		return format
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if env := Format(getenv(EnvOutput)); env != "" && slices.Contains(allowed, env) {
		return env
	}
	if IsAgent(getenv) {
		return FormatJSON
	}
	if tty {
		return ttyDefault
	}
	return pipeDefault
}

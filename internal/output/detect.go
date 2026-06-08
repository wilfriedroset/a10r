// SPDX-License-Identifier: Apache-2.0

package output

import "strings"

// Agent detection lets the CLI pick a machine-readable default format
// when invoked by an AI coding agent whose stdout is a real PTY (so the
// TTY probe would otherwise pick the human format). The marker set and
// precedence mirror the HuggingFace CLI's `_detect_agent.py` (itself
// inspired by @vercel/detect-agent); a10r adapts it under ADR 0045.
// Detection is presence-only: a marker set to an empty value does not
// count, matching the reference's truthiness check.

// standardAgentVars hold the agent name as their value: any tool may set
// them, and the value names the agent directly (unknown values still
// signal an agent, reported as "unknown").
var standardAgentVars = []string{"AI_AGENT", "AGENT"}

// toolAgent maps a set of tool-specific marker variables to the agent
// name reported when any of them is present. Order matters: cowork must
// precede claude-code so the more specific signal wins when both
// CLAUDE_CODE_IS_COWORK and CLAUDECODE are set.
type toolAgent struct {
	vars []string
	name string
}

var toolAgents = []toolAgent{
	{[]string{"ANTIGRAVITY_AGENT"}, "antigravity"},
	{[]string{"AUGMENT_AGENT"}, "augment-cli"},
	{[]string{"CLINE_ACTIVE"}, "cline"},
	{[]string{"CLAUDE_CODE_IS_COWORK"}, "cowork"},
	{[]string{"CLAUDECODE", "CLAUDE_CODE"}, "claude-code"},
	{[]string{"CODEX_SANDBOX", "CODEX_CI", "CODEX_THREAD_ID"}, "codex"},
	{[]string{"CURSOR_TRACE_ID"}, "cursor"},
	{[]string{"CURSOR_AGENT"}, "cursor-cli"},
	{[]string{"GEMINI_CLI"}, "gemini"},
	{[]string{"COPILOT_MODEL", "COPILOT_ALLOW_ALL", "COPILOT_GITHUB_TOKEN"}, "github-copilot"},
	{[]string{"GOOSE_TERMINAL"}, "goose"},
	{[]string{"OPENCLAW_SHELL"}, "openclaw"},
	{[]string{"OPENCODE_CLIENT"}, "opencode"},
	{[]string{"PI_CODING_AGENT"}, "pi"},
	{[]string{"REPL_ID"}, "replit"},
	{[]string{"ROO_ACTIVE"}, "roo-code"},
	{[]string{"TRAE_AI_SHELL_ID"}, "trae"},
}

// knownAgents is the set of names a standard-var value is matched
// against; an unrecognised value still signals an agent but is reported
// as "unknown". devin has no tool marker and is recognised only here.
var knownAgents = func() map[string]bool {
	m := map[string]bool{"devin": true}
	for _, ta := range toolAgents {
		m[ta.name] = true
	}
	return m
}()

// DetectAgent reports the name of the AI coding agent invoking the
// process, and whether one was detected at all. getenv is injected so
// callers pass os.Getenv in production and a stub in tests. Standard
// vars are checked first (their value is the name), then tool markers in
// declaration order; the first match wins.
func DetectAgent(getenv func(string) string) (string, bool) {
	for _, v := range standardAgentVars {
		name := strings.ToLower(strings.TrimSpace(getenv(v)))
		if name == "" {
			continue
		}
		if knownAgents[name] {
			return name, true
		}
		return "unknown", true
	}
	for _, ta := range toolAgents {
		for _, v := range ta.vars {
			if getenv(v) != "" {
				return ta.name, true
			}
		}
	}
	return "", false
}

// IsAgent reports whether the process is being invoked by an AI coding
// agent, for callers that only need the boolean.
func IsAgent(getenv func(string) string) bool {
	_, ok := DetectAgent(getenv)
	return ok
}

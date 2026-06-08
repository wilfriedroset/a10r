// SPDX-License-Identifier: Apache-2.0

package output

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func envGet(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// testAgentMarker is one known tool marker, used across the output tests to
// simulate a detected agent (any single marker would serve).
const testAgentMarker = "CLAUDECODE"

func TestDetectAgent(t *testing.T) {
	t.Parallel()

	t.Run("no markers is not an agent", func(t *testing.T) {
		t.Parallel()
		name, ok := DetectAgent(envGet(nil))
		require.False(t, ok)
		require.Empty(t, name)
		require.False(t, IsAgent(envGet(nil)))
	})

	// Driven off the production table so every marker is covered and the
	// test cannot drift from toolAgents.
	t.Run("each tool marker triggers its name", func(t *testing.T) {
		t.Parallel()
		for _, ta := range toolAgents {
			for _, v := range ta.vars {
				name, ok := DetectAgent(envGet(map[string]string{v: "1"}))
				require.True(t, ok, "%s should signal an agent", v)
				require.Equal(t, ta.name, name)
			}
		}
	})

	t.Run("standard var carries a known name", func(t *testing.T) {
		t.Parallel()
		known := toolAgents[0].name
		for _, sv := range standardAgentVars {
			name, ok := DetectAgent(envGet(map[string]string{sv: known}))
			require.True(t, ok)
			require.Equal(t, known, name)
		}
	})

	t.Run("standard var with an unrecognised value is unknown", func(t *testing.T) {
		t.Parallel()
		name, ok := DetectAgent(envGet(map[string]string{standardAgentVars[0]: "definitely-not-a-known-agent"}))
		require.True(t, ok)
		require.Equal(t, "unknown", name)
	})

	t.Run("standard var is trimmed and lowercased", func(t *testing.T) {
		t.Parallel()
		raw := "  " + strings.ToUpper(toolAgents[0].name) + "  "
		name, ok := DetectAgent(envGet(map[string]string{standardAgentVars[0]: raw}))
		require.True(t, ok)
		require.Equal(t, toolAgents[0].name, name)
	})

	t.Run("standard var wins over a tool marker", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			standardAgentVars[0]:  toolAgents[1].name,
			toolAgents[0].vars[0]: "1",
		}
		name, ok := DetectAgent(envGet(env))
		require.True(t, ok)
		require.Equal(t, toolAgents[1].name, name)
	})

	t.Run("empty standard var falls through to tool markers", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			standardAgentVars[0]:  "",
			toolAgents[0].vars[0]: "1",
		}
		name, ok := DetectAgent(envGet(env))
		require.True(t, ok)
		require.Equal(t, toolAgents[0].name, name)
	})

	t.Run("empty-valued tool marker does not count", func(t *testing.T) {
		t.Parallel()
		name, ok := DetectAgent(envGet(map[string]string{toolAgents[0].vars[0]: ""}))
		require.False(t, ok)
		require.Empty(t, name)
	})

	t.Run("more specific signal wins when ordered first", func(t *testing.T) {
		t.Parallel()
		// CLAUDE_CODE_IS_COWORK is declared before CLAUDECODE so cowork
		// takes priority when both are set (ADR 0045 marker ordering).
		env := map[string]string{"CLAUDE_CODE_IS_COWORK": "1", testAgentMarker: "1"}
		name, ok := DetectAgent(envGet(env))
		require.True(t, ok)
		require.Equal(t, "cowork", name)
	})
}

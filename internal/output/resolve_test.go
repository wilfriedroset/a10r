// SPDX-License-Identifier: Apache-2.0

package output

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveAgentAware(t *testing.T) {
	t.Parallel()

	listAllowed := []Format{FormatTable, FormatJSON, FormatYAML}
	thin := []Format{FormatJSON, FormatYAML} // get/write: no table

	cases := []struct {
		name        string
		format      Format
		env         map[string]string
		tty         bool
		allowed     []Format
		ttyDefault  Format
		pipeDefault Format
		want        Format
	}{
		{
			name:   "explicit flag wins over everything",
			format: FormatTable, env: map[string]string{EnvOutput: "yaml", testAgentMarker: "1"},
			tty: false, allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: FormatTable,
		},
		{
			// Garbage passthrough: a non-empty format is trusted to have
			// come from ParseFormat, so the caller's downstream switch
			// surfaces an unknown value rather than this silently defaulting.
			name:   "explicit garbage passes through unchanged",
			format: "csv", tty: true, allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: "csv",
		},
		{
			name: "A10R_OUTPUT used when valid for command",
			env:  map[string]string{EnvOutput: "yaml"},
			tty:  true, allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: FormatYAML,
		},
		{
			name: "A10R_OUTPUT invalid for command falls through (table on thin set)",
			env:  map[string]string{EnvOutput: "table"},
			tty:  false, allowed: thin, ttyDefault: FormatYAML, pipeDefault: FormatJSON,
			want: FormatJSON, // no agent, no tty -> pipe default
		},
		{
			name: "A10R_OUTPUT globally invalid falls through",
			env:  map[string]string{EnvOutput: "csv"},
			tty:  true, allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: FormatTable,
		},
		{
			name: "A10R_OUTPUT beats agent detection",
			env:  map[string]string{EnvOutput: "yaml", testAgentMarker: "1"},
			tty:  false, allowed: thin, ttyDefault: FormatYAML, pipeDefault: FormatJSON,
			want: FormatYAML,
		},
		{
			name: "agent detection selects json over tty default",
			env:  map[string]string{testAgentMarker: "1"},
			tty:  true, allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: FormatJSON,
		},
		{
			name:    "no env, no agent, tty -> tty default",
			tty:     true,
			allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: FormatTable,
		},
		{
			name:    "no env, no agent, pipe -> pipe default",
			tty:     false,
			allowed: listAllowed, ttyDefault: FormatTable, pipeDefault: FormatJSON,
			want: FormatJSON,
		},
		{
			name: "write-style: agent gets json, empty defaults are lines",
			env:  map[string]string{"CURSOR_TRACE_ID": "x"},
			tty:  false, allowed: thin, ttyDefault: "", pipeDefault: "",
			want: FormatJSON,
		},
		{
			name:    "write-style: no agent stays lines",
			tty:     false,
			allowed: thin, ttyDefault: "", pipeDefault: "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveAgentAware(tc.format, envGet(tc.env), tc.tty, tc.allowed, tc.ttyDefault, tc.pipeDefault)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveAgentAware_NilGetenvIsSafe(t *testing.T) {
	t.Parallel()
	got := ResolveAgentAware("", nil, true, []Format{FormatTable, FormatJSON}, FormatTable, FormatJSON)
	require.Equal(t, FormatTable, got)
}

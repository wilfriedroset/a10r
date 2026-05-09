// SPDX-License-Identifier: Apache-2.0

package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type alertRow struct {
	Name     string            `json:"name"`
	Severity string            `json:"severity"`
	Labels   map[string]string `json:"labels"`
}

func TestWriteJSON_StructPayload(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	payload := []alertRow{
		{Name: "HighCPU", Severity: "critical", Labels: map[string]string{"team": "plat"}},
	}
	require.NoError(t, WriteJSON(&buf, payload))

	out := buf.String()
	require.Contains(t, out, `"name": "HighCPU"`)
	require.Contains(t, out, `"severity": "critical"`)
	require.True(t, strings.HasSuffix(out, "\n"), "must end with a newline")
}

func TestWriteJSON_DoesNotEscapeHTML(t *testing.T) {
	t.Parallel()

	// Alert annotations frequently carry URLs with `&` / `<` / `>`;
	// SetEscapeHTML(false) keeps the wire form readable for jq users.
	// stdlib's default behaviour escapes those three runes as the
	// six-character sequences & / < / > — assert
	// neither the literal `&`/`<` is escaped (Contains) nor the
	// escape sequences appear (NotContains).
	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, map[string]string{
		"runbook": "https://wiki/runbook?team=plat&service=api&type=<crit>",
	}))
	out := buf.String()
	require.Contains(t, out, "team=plat&service=api&type=<crit>",
		"raw `&` and `<` survive — no HTML escaping")
	require.NotContains(t, out, `\u0026`, "`&` must not be unicode-escaped (default behaviour)")
	require.NotContains(t, out, `\u003c`, "`<` must not be unicode-escaped")
}

func TestWriteJSON_IndentTwoSpaces(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, map[string]int{"n": 1}))
	require.Contains(t, buf.String(), "\n  \"n\": 1")
}

func TestWriteJSON_WrapsEncodeError(t *testing.T) {
	t.Parallel()

	// encoding/json refuses unmarshallable types (chan, func,
	// complex). The wrapper must surface the error with a
	// "encode json:" prefix so a future grep / errors.As pass can
	// find the wrap site.
	var buf bytes.Buffer
	err := WriteJSON(&buf, make(chan int))
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode json:")
}

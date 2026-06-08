// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/doctor"
)

func cmdWithOutput(val string) *cobra.Command {
	c := &cobra.Command{Use: "x"}
	c.Flags().String("output", "", "")
	if val != "" {
		_ = c.Flags().Set("output", val)
	}
	return c
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRenderExecError(t *testing.T) {
	t.Parallel()

	t.Run("nil error writes nothing", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput(""), nil, envOf(nil), &buf)
		require.Empty(t, buf.String())
	})

	t.Run("explicit json flag yields an envelope", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput("json"), NewExitError(ExitConfigInvalid, errors.New("bad config")), envOf(nil), &buf)

		var got execError
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		require.Equal(t, execError{Error: "bad config", Code: ExitConfigInvalid}, got)
	})

	t.Run("yaml flag yields a yaml envelope", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput("yaml"), NewExitError(ExitNotFound, errors.New("nope")), envOf(nil), &buf)

		var got execError
		require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
		require.Equal(t, execError{Error: "nope", Code: ExitNotFound}, got)
	})

	t.Run("agent detection yields an envelope without a flag", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput(""), NewExitError(ExitUnreachable, errors.New("down")),
			envOf(map[string]string{"CLAUDECODE": "1"}), &buf)

		var got execError
		require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
		require.Equal(t, ExitUnreachable, got.Code)
	})

	t.Run("A10R_OUTPUT yields an envelope without a flag", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput(""), NewExitError(ExitConfigInvalid, errors.New("x")),
			envOf(map[string]string{"A10R_OUTPUT": "json"}), &buf)
		require.True(t, json.Valid(buf.Bytes()))
	})

	t.Run("human format falls back to plain message", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput("table"), NewExitError(ExitConfigInvalid, errors.New("boom")), envOf(nil), &buf)
		require.Equal(t, "boom\n", buf.String())
	})

	t.Run("no structured signal falls back to plain message", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput(""), NewExitError(ExitConfigInvalid, errors.New("boom")), envOf(nil), &buf)
		require.Equal(t, "boom\n", buf.String())
	})

	t.Run("already-emitted error is never enveloped", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput("json"), newEmittedError(ExitRuntimeError, errors.New("2 of 3 failed")), envOf(nil), &buf)
		require.Equal(t, "2 of 3 failed\n", buf.String())
		require.False(t, json.Valid(buf.Bytes()))
	})

	t.Run("--fail signal is never enveloped", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		renderExecError(cmdWithOutput("json"), NewExitError(ExitFailMatched, errors.New("--fail: 2 matched")), envOf(nil), &buf)
		require.Equal(t, "--fail: 2 matched\n", buf.String())
	})
}

// TestEmittedErrors_SkipEnvelope pins the wiring that keeps the two paths
// which write a structured result to stdout — write verbs and doctor — from
// being re-reported as a {error,code} envelope.
func TestEmittedErrors_SkipEnvelope(t *testing.T) {
	t.Parallel()

	werr := writeExitError([]writeResult{{Tenant: "p", Status: writeStatusError, Error: "boom"}}, nil)
	var we *ExitError
	require.ErrorAs(t, werr, &we)
	require.True(t, we.Emitted, "write failure must be Emitted (result array is on stdout)")

	derr := doctorExitFromResults(
		[]config.Backend{{Name: "p"}},
		[]doctor.Result{{Backend: "p", Check: checkAuth, Severity: doctor.SeverityError}},
	)
	var de *ExitError
	require.ErrorAs(t, derr, &de)
	require.True(t, de.Emitted, "doctor failure must be Emitted (report is on stdout)")
	require.Equal(t, ExitAuthFailed, de.Code)
}

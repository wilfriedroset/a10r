// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/output"
)

func TestResolveWriteFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		want    output.Format
		wantErr bool
	}{
		{name: "empty is lines", raw: "", want: ""},
		{name: "json explicit", raw: "json", want: output.FormatJSON},
		{name: "yaml explicit", raw: "yaml", want: output.FormatYAML},
		{name: "table rejected", raw: "table", wantErr: true},
		{name: "garbage rejected", raw: "csv", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveWriteFormat(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestEmitWriteResults_LinesModeSuccessToStdout(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer
	err := emitWriteResults(&out, &errOut, "", []writeResult{
		{Tenant: "prod", ID: "a1", Status: "created"},
		{Tenant: "staging", ID: "b2", Status: "created"},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "prod\ta1\nstaging\tb2\n", out.String())
	require.Empty(t, errOut.String())
}

func TestEmitWriteResults_LinesModeFailureToStderrNonZero(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer
	err := emitWriteResults(&out, &errOut, "", []writeResult{
		{Tenant: "prod", ID: "a1", Status: "created"},
		{Tenant: "staging", Status: "error", Error: "boom"},
	}, nil)
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitRuntimeError, ex.Code)
	require.Equal(t, "prod\ta1\n", out.String(), "only successes go to stdout")
	require.Contains(t, errOut.String(), "staging")
	require.Contains(t, errOut.String(), "boom")
}

func TestEmitWriteResults_AllUnreachableExits3(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer
	err := emitWriteResults(&out, &errOut, "", []writeResult{
		{Tenant: "prod", Status: "error", Error: "backend unreachable"},
		{Tenant: "staging", Status: "error", Error: "backend unreachable"},
	}, []error{backend.ErrUnreachable, backend.ErrUnreachable})
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitUnreachable, ex.Code,
		"every write failing unreachable surfaces code 3, like the reads and sibling verbs")
}

func TestEmitWriteResults_MixedFailuresStayRuntimeError(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer
	err := emitWriteResults(&out, &errOut, "", []writeResult{
		{Tenant: "prod", ID: "a1", Status: "created"},
		{Tenant: "staging", Status: "error", Error: "backend unreachable"},
	}, []error{nil, backend.ErrUnreachable})
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitRuntimeError, ex.Code, "a partial failure is not a uniform transport class")
}

func TestEmitWriteResults_JSONModeFullArray(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer
	err := emitWriteResults(&out, &errOut, output.FormatJSON, []writeResult{
		{Tenant: "prod", ID: "a1", Status: "created"},
		{Tenant: "staging", Status: "error", Error: "boom"},
	}, nil)
	require.Error(t, err) // a failure present → non-zero even in json mode

	var got []writeResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "created", got[0].Status)
	require.Equal(t, "error", got[1].Status)
}

func TestResolveCreator(t *testing.T) {
	t.Parallel()
	require.Equal(t, "alice", resolveCreator("alice", "bob"))
	require.Equal(t, "bob", resolveCreator("", "bob"))
	require.Equal(t, defaultCreator, resolveCreator("", ""))
}

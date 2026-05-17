// SPDX-License-Identifier: Apache-2.0

package bulkop_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
)

// TestDoneMsg_Successes_FiltersByErr pins the contract that
// DoneMsg.Successes returns the keys of every Result whose Err is
// nil, in input order, and omits failures (including
// ErrNoWriteableBackend) and unstarted-due-cancel.
func TestDoneMsg_Successes_FiltersByErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		results []bulkop.Result[string]
		want    []string
	}{
		{
			name:    "empty results returns empty slice",
			results: nil,
			want:    []string{},
		},
		{
			name: "all-success returns every key in order",
			results: []bulkop.Result[string]{
				{Op: bulkop.Op[string]{Key: "a", Tenant: "prod"}},
				{Op: bulkop.Op[string]{Key: "b", Tenant: "prod"}},
			},
			want: []string{"a", "b"},
		},
		{
			name: "mixed success and writer error",
			results: []bulkop.Result[string]{
				{Op: bulkop.Op[string]{Key: "a", Tenant: "prod"}},
				{Op: bulkop.Op[string]{Key: "b", Tenant: "prod"}, Err: errors.New("boom")},
				{Op: bulkop.Op[string]{Key: "c", Tenant: "prod"}},
			},
			want: []string{"a", "c"},
		},
		{
			name: "ErrNoWriteableBackend counts as failure",
			results: []bulkop.Result[string]{
				{Op: bulkop.Op[string]{Key: "a", Tenant: "prod"}, Err: bulkop.ErrNoWriteableBackend},
				{Op: bulkop.Op[string]{Key: "b", Tenant: "staging"}},
			},
			want: []string{"b"},
		},
		{
			name: "all-failure returns empty slice",
			results: []bulkop.Result[string]{
				{Op: bulkop.Op[string]{Key: "a", Tenant: "prod"}, Err: errors.New("x")},
				{Op: bulkop.Op[string]{Key: "b", Tenant: "prod"}, Err: errors.New("y")},
			},
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := bulkop.DoneMsg[string]{Results: tc.results}.Successes()
			require.Equal(t, tc.want, got)
		})
	}
}

// TestOp_GenericKeyTypes pins that Op[K] / Result[K] / DoneMsg[K]
// instantiate with the two key types the call sites use today — an
// alert-fingerprint string and a silence-ID string. The two stay
// untyped-string under the hood but the type parameter keeps the
// API boundary explicit; this test compiles into a no-op once the
// constraints are right but breaks at vet/build time if a future
// signature change drops the comparable bound or shifts the
// parameter ordering.
func TestOp_GenericKeyTypes(t *testing.T) {
	t.Parallel()

	type alertFP string
	type silenceID string

	fpOp := bulkop.Op[alertFP]{Key: "fp-a", Tenant: "prod"}
	sidOp := bulkop.Op[silenceID]{Key: "sil-a", Tenant: "prod"}
	require.Equal(t, alertFP("fp-a"), fpOp.Key)
	require.Equal(t, silenceID("sil-a"), sidOp.Key)

	fpDone := bulkop.DoneMsg[alertFP]{Results: []bulkop.Result[alertFP]{{Op: fpOp}}}
	sidDone := bulkop.DoneMsg[silenceID]{Results: []bulkop.Result[silenceID]{{Op: sidOp}}}
	require.Equal(t, []alertFP{"fp-a"}, fpDone.Successes())
	require.Equal(t, []silenceID{"sil-a"}, sidDone.Successes())
}

// SPDX-License-Identifier: Apache-2.0

package bulkop_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
)

// recordingWriter captures every (tenant, op) the dispatcher hands
// it. Safe for concurrent use — the dispatcher invokes workers
// across goroutines so the mutex is non-optional.
type recordingWriter struct {
	mu     sync.Mutex
	calls  []bulkop.Op[string]
	errOn  map[string]error
	ackFor map[string]string
}

func (w *recordingWriter) write(_ context.Context, _ string, op bulkop.Op[string]) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, op)
	return w.ackFor[op.Key], w.errOn[op.Key]
}

func runCmd(t *testing.T, cmd tea.Cmd) bulkop.DoneMsg[string] {
	t.Helper()
	require.NotNil(t, cmd)
	msg := cmd()
	done, ok := msg.(bulkop.DoneMsg[string])
	require.True(t, ok, "dispatch must emit bulkop.DoneMsg[string], got %T", msg)
	return done
}

func keys(rs []bulkop.Result[string]) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Op.Key)
	}
	sort.Strings(out)
	return out
}

func TestDispatch_TableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ops         []bulkop.Op[string]
		errOn       map[string]error
		ackFor      map[string]string
		concurrency int
		want        []string
		wantErrs    map[string]bool
	}{
		{
			name:        "empty ops emits empty DoneMsg",
			ops:         nil,
			concurrency: 4,
			want:        []string{},
		},
		{
			name: "multiple ops single tenant",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
				{Key: "b", Tenant: "prod"},
				{Key: "c", Tenant: "prod"},
			},
			concurrency: 4,
			want:        []string{"a", "b", "c"},
		},
		{
			name: "multiple ops multiple tenants",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
				{Key: "b", Tenant: "staging"},
				{Key: "c", Tenant: "prod"},
				{Key: "d", Tenant: "staging"},
			},
			concurrency: 2,
			want:        []string{"a", "b", "c", "d"},
		},
		{
			name: "writer error keeps op in results with err set",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
				{Key: "b", Tenant: "prod"},
			},
			errOn:       map[string]error{"b": errors.New("boom")},
			concurrency: 2,
			want:        []string{"a"},
			wantErrs:    map[string]bool{"b": true},
		},
		{
			name: "writer ack surfaces on Result.Ack",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
			},
			ackFor:      map[string]string{"a": "server-id-7"},
			concurrency: 2,
			want:        []string{"a"},
		},
		{
			name: "ErrNoWriteableBackend counts as failure",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
				{Key: "b", Tenant: "ghost"},
			},
			errOn:       map[string]error{"b": bulkop.ErrNoWriteableBackend},
			concurrency: 2,
			want:        []string{"a"},
			wantErrs:    map[string]bool{"b": true},
		},
		{
			name: "concurrency=1 still completes all ops",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
				{Key: "b", Tenant: "prod"},
				{Key: "c", Tenant: "prod"},
			},
			concurrency: 1,
			want:        []string{"a", "b", "c"},
		},
		{
			name: "concurrency=0 floors at 1 (max(1, min(0, n)))",
			ops: []bulkop.Op[string]{
				{Key: "a", Tenant: "prod"},
				{Key: "b", Tenant: "prod"},
			},
			concurrency: 0,
			want:        []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &recordingWriter{errOn: tc.errOn, ackFor: tc.ackFor}
			done := runCmd(t, bulkop.Dispatch(context.Background(), tc.ops, w.write, tc.concurrency))
			require.Len(t, done.Results, len(tc.ops),
				"DoneMsg.Results must have one entry per submitted Op")
			require.ElementsMatch(t, tc.want, done.Successes(),
				"Successes must list every err==nil op key")
			for _, r := range done.Results {
				if tc.wantErrs[r.Op.Key] {
					require.Error(t, r.Err, "op %q must surface its writer error", r.Op.Key)
				} else {
					require.NoError(t, r.Err, "op %q must succeed", r.Op.Key)
				}
				if want, ok := tc.ackFor[r.Op.Key]; ok && r.Err == nil {
					require.Equal(t, want, r.Ack, "op %q must surface the writer's ack", r.Op.Key)
				}
			}
			w.mu.Lock()
			require.Len(t, w.calls, len(tc.ops),
				"writer must have been invoked once per op")
			w.mu.Unlock()
		})
	}
}

// TestDispatch_PerTenantConcurrencyCap pins that the per-tenant
// worker pool actually caps in-flight Writer calls at the configured
// concurrency. Two tenants with three ops each at concurrency=2 must
// peak at no more than 2 in-flight per tenant — but tenants run in
// parallel, so the global peak is 4.
func TestDispatch_PerTenantConcurrencyCap(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	inFlight := map[string]int{}
	peak := map[string]int{}
	enter := func(tenant string) {
		mu.Lock()
		inFlight[tenant]++
		if inFlight[tenant] > peak[tenant] {
			peak[tenant] = inFlight[tenant]
		}
		mu.Unlock()
	}
	leave := func(tenant string) {
		mu.Lock()
		inFlight[tenant]--
		mu.Unlock()
	}

	writer := func(_ context.Context, tenant string, _ bulkop.Op[string]) (string, error) {
		enter(tenant)
		time.Sleep(10 * time.Millisecond)
		leave(tenant)
		return "", nil
	}
	ops := []bulkop.Op[string]{
		{Key: "a1", Tenant: "prod"},
		{Key: "a2", Tenant: "prod"},
		{Key: "a3", Tenant: "prod"},
		{Key: "b1", Tenant: "stg"},
		{Key: "b2", Tenant: "stg"},
		{Key: "b3", Tenant: "stg"},
	}
	done := runCmd(t, bulkop.Dispatch(context.Background(), ops, writer, 2))
	require.Len(t, done.Successes(), len(ops))
	mu.Lock()
	defer mu.Unlock()
	require.LessOrEqual(t, peak["prod"], 2, "per-tenant in-flight must cap at concurrency")
	require.LessOrEqual(t, peak["stg"], 2, "per-tenant in-flight must cap at concurrency")
}

// TestDispatch_ContextCancel_ShortCircuitsProducer asserts that
// cancelling ctx while a slow writer is in flight stops the producer
// from feeding additional work. Ops the producer drops land in
// Results with Err==ctx.Err so the total count is conserved.
func TestDispatch_ContextCancel_ShortCircuitsProducer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gate := make(chan struct{})
	var inFlt atomic.Int32
	writer := func(ctx context.Context, _ string, _ bulkop.Op[string]) (string, error) {
		inFlt.Add(1)
		defer inFlt.Add(-1)
		select {
		case <-gate:
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	ops := make([]bulkop.Op[string], 10)
	for i := range ops {
		ops[i] = bulkop.Op[string]{Key: fmt.Sprintf("k-%d", i), Tenant: "prod"}
	}
	cmd := bulkop.Dispatch(ctx, ops, writer, 1)
	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- cmd() }()

	require.Eventually(t, func() bool { return inFlt.Load() >= 1 }, time.Second, time.Millisecond,
		"at least one writer must reach the gate")
	cancel()
	close(gate)
	msg := <-doneCh
	done := msg.(bulkop.DoneMsg[string])
	require.Len(t, done.Results, len(ops),
		"DoneMsg.Results must conserve every submitted Op even on cancel")
	require.Less(t, len(done.Successes()), len(ops),
		"cancel must short-circuit at least one op")
}

// TestDispatch_NoClientForTenant_WriterEmitsSentinel walks the
// integration shape page Writer closures use: a missing client map
// entry surfaces as ErrNoWriteableBackend on Result.Err, the
// dispatcher does not error globally, and every op for that tenant
// still lands in Results.
func TestDispatch_NoClientForTenant_WriterEmitsSentinel(t *testing.T) {
	t.Parallel()

	clients := map[string]string{"prod": "real-client"}
	writer := func(_ context.Context, tenant string, _ bulkop.Op[string]) (string, error) {
		if _, ok := clients[tenant]; !ok {
			return "", bulkop.ErrNoWriteableBackend
		}
		return "", nil
	}
	ops := []bulkop.Op[string]{
		{Key: "a", Tenant: "prod"},
		{Key: "b", Tenant: "ghost"},
		{Key: "c", Tenant: "ghost"},
		{Key: "d", Tenant: "prod"},
	}
	done := runCmd(t, bulkop.Dispatch(context.Background(), ops, writer, 4))
	require.ElementsMatch(t, []string{"a", "d"}, done.Successes())
	require.ElementsMatch(t, []string{"a", "b", "c", "d"}, keys(done.Results))
	for _, r := range done.Results {
		if r.Op.Tenant == "ghost" {
			require.ErrorIs(t, r.Err, bulkop.ErrNoWriteableBackend)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0

package multi

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/backendtest"
)

// fakeClient satisfies backend.Client for fan-out tests. The
// hookable ListAlerts override lets each test inject behaviour
// (succeed, fail, observe in-flight count) without per-test mock
// scaffolding. Other Client methods come from the embedded stub
// and return ErrUnsupported — none of these tests touch them.
type fakeClient struct {
	backendtest.ClientStub
	listAlertsFn func(ctx context.Context) ([]backend.Alert, error)
}

// Compile-time assertion: every test fake must satisfy the full
// backend.Client. Catches a future interface change loud.
var _ backend.Client = (*fakeClient)(nil)

func (f *fakeClient) ListAlerts(ctx context.Context, _ backend.AlertFilter) ([]backend.Alert, error) {
	if f.listAlertsFn != nil {
		return f.listAlertsFn(ctx)
	}
	return nil, nil
}

func TestClient_OneFailsOthersSucceed(t *testing.T) {
	t.Parallel()

	failure := errors.New("simulated outage")
	tenants := []TenantClient{
		{Name: "prod", Client: &fakeClient{listAlertsFn: func(context.Context) ([]backend.Alert, error) {
			return nil, failure
		}}},
		{Name: "staging", Client: &fakeClient{listAlertsFn: func(context.Context) ([]backend.Alert, error) {
			return []backend.Alert{{Fingerprint: "s1"}}, nil
		}}},
	}

	results := New(tenants, 4).ListAlerts(t.Context(), backend.AlertFilter{})
	require.Len(t, results, 2)
	require.Equal(t, "prod", results[0].Tenant)
	require.ErrorIs(t, results[0].Err, failure)
	require.Empty(t, results[0].Value)
	require.NoError(t, results[1].Err)
	require.Len(t, results[1].Value, 1)
}

func TestClient_EmptyTenantList(t *testing.T) {
	t.Parallel()

	results := New(nil, 4).ListAlerts(t.Context(), backend.AlertFilter{})
	require.Empty(t, results, "no tenants → empty slice, not nil-vs-empty drift")
}

func TestClient_ResultsOrderMatchesTenantOrder(t *testing.T) {
	t.Parallel()

	// 5 tenants, each parked on its gate; gates closed in reverse so
	// completion order is e,d,c,b,a. Result order must still match
	// input order.
	names := []string{"a", "b", "c", "d", "e"}
	gates := map[string]chan struct{}{}
	for _, n := range names {
		gates[n] = make(chan struct{})
	}
	started := make(chan struct{}, len(names))
	mkTenant := func(name string) TenantClient {
		return TenantClient{
			Name: name,
			Client: &fakeClient{listAlertsFn: func(_ context.Context) ([]backend.Alert, error) {
				started <- struct{}{}
				<-gates[name]
				return []backend.Alert{{Fingerprint: name}}, nil
			}},
		}
	}
	tenants := make([]TenantClient, 0, len(names))
	for _, n := range names {
		tenants = append(tenants, mkTenant(n))
	}

	resCh := make(chan []Result[[]backend.Alert], 1)
	go func() {
		resCh <- New(tenants, len(names)).ListAlerts(t.Context(), backend.AlertFilter{})
	}()
	for range names {
		<-started
	}
	for _, n := range []string{"e", "d", "c", "b", "a"} {
		close(gates[n])
	}
	results := <-resCh

	require.Len(t, results, 5)
	for i, want := range names {
		require.Equal(t, want, results[i].Tenant)
		require.NoError(t, results[i].Err)
		require.Len(t, results[i].Value, 1)
		require.Equal(t, want, results[i].Value[0].Fingerprint)
	}
}

func TestClient_PoolSizeBoundsParallelism(t *testing.T) {
	t.Parallel()

	var inFlight, peak atomic.Int32
	const tenantCount = 20
	const poolSize = 4

	started := make(chan struct{}, tenantCount)
	release := make(chan struct{})
	mkClient := func() backend.Client {
		return &fakeClient{listAlertsFn: func(context.Context) ([]backend.Alert, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			started <- struct{}{}
			<-release
			return nil, nil
		}}
	}
	tenants := make([]TenantClient, tenantCount)
	for i := range tenants {
		tenants[i] = TenantClient{Name: "t", Client: mkClient()}
	}

	resCh := make(chan []Result[[]backend.Alert], 1)
	go func() {
		resCh <- New(tenants, poolSize).ListAlerts(t.Context(), backend.AlertFilter{})
	}()
	for range poolSize {
		<-started
	}
	require.Equal(t, int32(poolSize), peak.Load(),
		"exactly poolSize workers must be active before release")
	close(release)
	results := <-resCh

	require.Len(t, results, tenantCount)
}

func TestClient_ZeroPoolSizeUsesDefault(t *testing.T) {
	t.Parallel()

	mc := New([]TenantClient{}, 0)
	require.Equal(t, defaultPoolSize, mc.poolSize, "zero pool size must default")

	mc2 := New([]TenantClient{}, -5)
	require.Equal(t, defaultPoolSize, mc2.poolSize, "negative pool size must default")
}

func TestClient_ContextCancelMarksRemaining(t *testing.T) {
	t.Parallel()

	// 4 tenants, pool size 1: only one runs at a time, so cancelling
	// after the first finishes should mark the rest as cancelled
	// without ever running them.
	started := atomic.Int32{}
	tenant := func(name string, delay time.Duration) TenantClient {
		return TenantClient{
			Name: name,
			Client: &fakeClient{listAlertsFn: func(ctx context.Context) ([]backend.Alert, error) {
				started.Add(1)
				select {
				case <-time.After(delay):
					return nil, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}},
		}
	}
	tenants := []TenantClient{
		tenant("a", 1*time.Millisecond),
		tenant("b", time.Hour),
		tenant("c", time.Hour),
		tenant("d", time.Hour),
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	results := New(tenants, 1).ListAlerts(ctx, backend.AlertFilter{})

	require.Len(t, results, 4)
	// Every result has a Tenant name (no silent dropping).
	for i, r := range results {
		require.Equal(t, tenants[i].Name, r.Tenant)
	}
	// First tenant finished cleanly before cancel; the rest carry
	// errors (either ctx.Err from the dispatcher's select, or
	// ctx.Err from the underlying op).
	require.NoError(t, results[0].Err)
	require.Error(t, results[1].Err)
	require.Error(t, results[2].Err)
	require.Error(t, results[3].Err)
}

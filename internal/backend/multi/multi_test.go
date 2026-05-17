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
)

// fakeClient satisfies backend.Client for fan-out tests. The
// hookable ListAlerts override lets each test inject behaviour
// (succeed, fail, observe in-flight count) without per-test mock
// scaffolding.
type fakeClient struct {
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

func (*fakeClient) ListAlertGroups(context.Context, backend.AlertFilter) ([]backend.AlertGroup, error) {
	return nil, nil
}

func (*fakeClient) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	return nil, nil
}

func (*fakeClient) GetSilence(context.Context, string) (backend.Silence, error) {
	return backend.Silence{}, nil
}

func (*fakeClient) ListReceivers(context.Context) ([]backend.Receiver, error) {
	return nil, nil
}

func (*fakeClient) Status(context.Context) (backend.Status, error) {
	return backend.Status{}, nil
}

func (*fakeClient) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	return "", nil
}
func (*fakeClient) UpdateSilence(context.Context, string, backend.SilenceSpec) error { return nil }
func (*fakeClient) ExpireSilence(context.Context, string) error                      { return nil }
func (*fakeClient) GetConfig(context.Context) (backend.MimirConfig, error) {
	return backend.MimirConfig{}, backend.ErrUnsupported
}

func (*fakeClient) SetConfig(context.Context, backend.MimirConfig) error {
	return backend.ErrUnsupported
}
func (*fakeClient) DeleteConfig(context.Context) error { return backend.ErrUnsupported }
func (*fakeClient) ListTenantConfigs(context.Context) ([]backend.TenantConfig, error) {
	return nil, backend.ErrUnsupported
}

func (*fakeClient) RingStatus(context.Context) (backend.Ring, error) {
	return backend.Ring{}, backend.ErrUnsupported
}
func (*fakeClient) Capabilities() backend.Caps { return backend.Caps{} }

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

	// 5 tenants, fast and slow mixed; order of Result entries must
	// match input order even if completion order is different.
	mkTenant := func(name string, delay time.Duration) TenantClient {
		return TenantClient{
			Name: name,
			Client: &fakeClient{listAlertsFn: func(_ context.Context) ([]backend.Alert, error) {
				time.Sleep(delay)
				return []backend.Alert{{Fingerprint: name}}, nil
			}},
		}
	}
	tenants := []TenantClient{
		mkTenant("a", 30*time.Millisecond),
		mkTenant("b", 5*time.Millisecond),
		mkTenant("c", 20*time.Millisecond),
		mkTenant("d", 10*time.Millisecond),
		mkTenant("e", 1*time.Millisecond),
	}

	results := New(tenants, 5).ListAlerts(t.Context(), backend.AlertFilter{})
	require.Len(t, results, 5)
	for i, want := range []string{"a", "b", "c", "d", "e"} {
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

	mkClient := func() backend.Client {
		return &fakeClient{listAlertsFn: func(context.Context) ([]backend.Alert, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			// Update peak (CAS loop, race-detector clean).
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			return nil, nil
		}}
	}
	tenants := make([]TenantClient, tenantCount)
	for i := range tenants {
		tenants[i] = TenantClient{Name: "t", Client: mkClient()}
	}

	results := New(tenants, poolSize).ListAlerts(t.Context(), backend.AlertFilter{})
	require.Len(t, results, tenantCount)
	require.LessOrEqual(t, peak.Load(), int32(poolSize),
		"peak in-flight (%d) must not exceed pool size (%d)", peak.Load(), poolSize)
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

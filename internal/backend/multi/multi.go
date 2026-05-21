// SPDX-License-Identifier: Apache-2.0

// Package multi orchestrates fan-out across N per-tenant
// backend.Clients. The multi-tenant TUI affordance (selecting "all"
// or a subset of tenants) needs one place that runs the same call
// against every tenant, bounds concurrency, and surfaces per-tenant
// errors without ever silently swallowing one.
//
// Client is deliberately NOT a backend.Client — it returns
// slices of per-tenant Result values rather than single returns.
// Single-tenant operations (GetSilence, write methods, capability
// methods) stay on backend.Client; the caller picks the tenant.
package multi

import (
	"context"
	"sync"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// defaultPoolSize bounds parallel fan-outs when New is called with
// poolSize <= 0. Tuned for the current expected fan-out size; the
// cap can grow with workload evidence.
const defaultPoolSize = 8

// TenantClient pairs a name (typically backend.Name from a10r.yaml)
// with a constructed Client. The name flows through Result.Tenant
// so the TUI can render "prod-am: unauthorized; staging: …" in the
// flash strip.
type TenantClient struct {
	Name   string
	Client backend.Client
}

// Client runs read-only operations across every TenantClient
// in parallel, bounded by poolSize. Construct via New; safe for
// concurrent use across goroutines.
type Client struct {
	tenants  []TenantClient
	poolSize int
}

// New constructs a Client. A non-positive poolSize defaults to
// defaultPoolSize so callers passing zero get sensible behaviour.
func New(tenants []TenantClient, poolSize int) *Client {
	if poolSize <= 0 {
		poolSize = defaultPoolSize
	}
	return &Client{tenants: tenants, poolSize: poolSize}
}

// Result carries one tenant's outcome from a fan-out call. Err is
// non-nil when that tenant's call failed; Value is the zero value
// of V in that case. Successful tenants have Err == nil and Value
// populated. Result entries are returned in the same order as the
// TenantClient slice passed to New, so the caller can correlate by
// index without consulting Tenant.
type Result[V any] struct {
	Tenant string
	Value  V
	Err    error
}

// ListAlerts fans out backend.Reader.ListAlerts to every tenant.
// Returns one Result per tenant in declaration order; errors are
// per-tenant and never silently swallowed.
func (m *Client) ListAlerts(ctx context.Context, filter backend.AlertFilter) []Result[[]backend.Alert] {
	return fanOut(ctx, m.tenants, m.poolSize, func(ctx context.Context, c backend.Client) ([]backend.Alert, error) {
		return c.ListAlerts(ctx, filter)
	})
}

// ListAlertGroups fans out backend.Reader.ListAlertGroups.
func (m *Client) ListAlertGroups(ctx context.Context, filter backend.AlertFilter) []Result[[]backend.AlertGroup] {
	return fanOut(ctx, m.tenants, m.poolSize, func(ctx context.Context, c backend.Client) ([]backend.AlertGroup, error) {
		return c.ListAlertGroups(ctx, filter)
	})
}

// ListSilences fans out backend.Reader.ListSilences.
func (m *Client) ListSilences(ctx context.Context, filter backend.SilenceFilter) []Result[[]backend.Silence] {
	return fanOut(ctx, m.tenants, m.poolSize, func(ctx context.Context, c backend.Client) ([]backend.Silence, error) {
		return c.ListSilences(ctx, filter)
	})
}

// ListReceivers fans out backend.Reader.ListReceivers.
func (m *Client) ListReceivers(ctx context.Context) []Result[[]backend.Receiver] {
	return fanOut(ctx, m.tenants, m.poolSize, func(ctx context.Context, c backend.Client) ([]backend.Receiver, error) {
		return c.ListReceivers(ctx)
	})
}

// Status fans out backend.Reader.Status.
func (m *Client) Status(ctx context.Context) []Result[backend.Status] {
	return fanOut(ctx, m.tenants, m.poolSize, func(ctx context.Context, c backend.Client) (backend.Status, error) {
		return c.Status(ctx)
	})
}

// fanOut runs op(ctx, client) for every tenant in parallel under a
// poolSize bound. The returned slice has one entry per tenant in
// declaration order; tenants whose dispatch was blocked by a
// cancelled ctx surface ctx.Err() in their Result.Err.
//
// op is called with the caller's ctx unmodified — derived contexts
// (deadlines, cancellation) are the caller's responsibility.
func fanOut[V any](
	ctx context.Context,
	tenants []TenantClient,
	poolSize int,
	op func(context.Context, backend.Client) (V, error),
) []Result[V] {
	out := make([]Result[V], len(tenants))
	sem := make(chan struct{}, poolSize)
	var wg sync.WaitGroup

	for i, t := range tenants {
		select {
		case <-ctx.Done():
			// Mark the remaining tenants as cancelled so the caller
			// sees a complete per-tenant accounting rather than a
			// short slice.
			for j := i; j < len(tenants); j++ {
				out[j] = Result[V]{Tenant: tenants[j].Name, Err: ctx.Err()}
			}
			wg.Wait()
			return out
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(i int, t TenantClient) {
			defer wg.Done()
			defer func() { <-sem }()

			v, err := op(ctx, t.Client)
			out[i] = Result[V]{Tenant: t.Name, Value: v, Err: err}
		}(i, t)
	}

	wg.Wait()
	return out
}

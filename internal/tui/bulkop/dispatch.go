// SPDX-License-Identifier: Apache-2.0

package bulkop

import (
	"context"
	"sync"
)

// run is the worker-pool implementation that backs Dispatch. Split
// out of the tea.Cmd-returning Dispatch so the goroutine machinery
// is unit-testable without a tea runtime in scope.
func run[K comparable](
	ctx context.Context,
	ops []Op[K],
	writer Writer[K],
	concurrency int,
) []Result[K] {
	if len(ops) == 0 {
		return nil
	}
	byTenant := map[string][]Op[K]{}
	tenants := []string{}
	for _, op := range ops {
		if _, seen := byTenant[op.Tenant]; !seen {
			tenants = append(tenants, op.Tenant)
		}
		byTenant[op.Tenant] = append(byTenant[op.Tenant], op)
	}
	resCh := make(chan Result[K], len(ops))
	var tenantWg sync.WaitGroup
	for _, tenant := range tenants {
		tenantWg.Add(1)
		go func(tenant string, tOps []Op[K]) {
			defer tenantWg.Done()
			runTenantPool(ctx, tenant, tOps, writer, concurrency, resCh)
		}(tenant, byTenant[tenant])
	}
	go func() {
		tenantWg.Wait()
		close(resCh)
	}()
	out := make([]Result[K], 0, len(ops))
	for r := range resCh {
		out = append(out, r)
	}
	return out
}

// runTenantPool is the per-tenant bounded worker pool. Producer
// feeds the jobs channel under ctx.Done so a mid-flight cancel
// stops dispatching new work; consumers run the Writer and emit
// results regardless of ctx state for jobs they have already
// pulled, so an in-flight request completes naturally. Workers cap
// at max(1, min(concurrency, len(ops))).
//
// Jobs the producer drops on ctx.Done land in resCh with Err set
// to ctx.Err after the worker pool drains, so the aggregated
// DoneMsg has exactly one Result per submitted Op — pages tell
// "unstarted-due-cancel" apart from "writer returned nil" via the
// Err field.
func runTenantPool[K comparable](
	ctx context.Context,
	tenant string,
	ops []Op[K],
	writer Writer[K],
	concurrency int,
	resCh chan<- Result[K],
) {
	workers := max(min(concurrency, len(ops)), 1)
	jobs := make(chan Op[K])
	skipped := make(chan []Op[K], 1)
	go func() {
		defer close(jobs)
		for i, op := range ops {
			select {
			case <-ctx.Done():
				skipped <- ops[i:]
				return
			case jobs <- op:
			}
		}
		skipped <- nil
	}()
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for op := range jobs {
				ack, err := writer(ctx, tenant, op)
				resCh <- Result[K]{Op: op, Ack: ack, Err: err}
			}
		})
	}
	wg.Wait()
	for _, op := range <-skipped {
		resCh <- Result[K]{Op: op, Err: ctx.Err()}
	}
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
)

// backendResult pairs one backend's outcome with the tenant it came
// from. Exactly one of value / err carries meaning: err set means the
// build or op failed for that backend, value is the zero T.
type backendResult[T any] struct {
	tenant string
	value  T
	err    error
}

// fanOutBackends runs op against every backend in cfg.Backends in
// parallel, building one client per backend via build, and returns the
// per-backend outcomes in config order so callers render
// deterministically without re-sorting.
//
// Distinct from listcmd.fanOut, which bakes in row concatenation and
// the "every backend failed" boolean for the list pipeline. The get /
// silence-write verbs need the raw per-backend outcomes to apply their
// own lenient (get) or fail-closed (write) aggregation policy, so this
// primitive stops at collection.
func fanOutBackends[T any](
	ctx context.Context,
	cfg *config.Config,
	build listcmd.ClientFactory,
	op func(ctx context.Context, tenant string, c backend.Client) (T, error),
) []backendResult[T] {
	results := make([]backendResult[T], len(cfg.Backends))
	g, gctx := errgroup.WithContext(ctx)
	for i, be := range cfg.Backends {
		g.Go(func() error {
			results[i].tenant = be.Name
			c, err := build(be)
			if err != nil {
				results[i].err = fmt.Errorf("backend %q: build: %w", be.Name, err)
				return nil
			}
			v, err := op(gctx, be.Name, c)
			if err != nil {
				results[i].err = fmt.Errorf("backend %q: %w", be.Name, err)
				return nil
			}
			results[i].value = v
			return nil
		})
	}
	// Wait never errors: every goroutine returns nil and captures its
	// own failure into results[i] for the caller's aggregation.
	_ = g.Wait()
	return results
}

// emitBackendErrors prints per-backend failures to errOut sorted by
// message — which, given fanOutBackends prefixes every error with
// `backend %q:`, is backend-name order — and reports the failed/total
// counts so the caller can branch on "every backend failed". Mirrors
// the list pipeline's stderr-warning contract for partial failure.
func emitBackendErrors[T any](errOut io.Writer, results []backendResult[T]) (failed, total int) {
	var msgs []string
	for _, r := range results {
		total++
		if r.err != nil {
			failed++
			msgs = append(msgs, r.err.Error())
		}
	}
	sort.Strings(msgs)
	for _, m := range msgs {
		fmt.Fprintln(errOut, m)
	}
	return failed, total
}

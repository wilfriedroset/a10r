// SPDX-License-Identifier: Apache-2.0

package listcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/wilfriedroset/a10r/internal/output"
)

// Run executes the headless list pipeline for one command. The body
// is a sequential list of stages, each with a single load-bearing
// concern; the comments call out the *why* per stage, not the *what*
// the code already shows. Pre-resolved seams (PagerFactory, Stderr)
// are applied once up front so the stage code reads with intent.
//
// Returns:
//   - nil on success.
//   - ctx.Err() when cancelled mid-fetch.
//   - render or pager errors unwrapped (the cmd layer prints them).
//   - ErrAllBackendsFailed wrapped with a label-derived message when
//     every backend in scope failed.
//   - ErrMatched wrapped with the count + label when FailOnAny is set
//     and the post-fetch row slice is non-empty.
func Run[R any](ctx context.Context, spec Spec[R]) error {
	if spec.Deps.BuildClient == nil {
		return errors.New("listcmd: Spec.Deps.BuildClient is required")
	}
	deps := spec.Deps.resolved()

	tty := isTerminal(spec.Out)
	resolved := output.Resolve(spec.Format, tty)
	renderer, ok := spec.Renderers[resolved]
	if !ok {
		return fmt.Errorf("listcmd: no renderer for format %q", resolved)
	}

	rows, allFailed, fetchErrs := fanOut(ctx, spec, deps)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fetch cancelled: %w", err)
	}

	// Deterministic stderr ordering: errgroup completes goroutines in
	// scheduler order, so without an explicit sort two runs of the
	// same fixture can print backends in different orders. Sorting by
	// the error string is a proxy for sorting by backend name —
	// fanOut prefixes every error with `backend %q:` so the natural
	// string order is the backend-name order.
	sort.Slice(fetchErrs, func(i, j int) bool { return fetchErrs[i].Error() < fetchErrs[j].Error() })
	for _, e := range fetchErrs {
		fmt.Fprintln(deps.Stderr, e)
	}

	if spec.Sort != nil {
		spec.Sort(rows)
	}

	pager, err := deps.PagerFactory(ctx, spec.Out, tty && resolved == output.FormatTable, spec.NoPager)
	if err != nil {
		return err
	}
	if err := renderer(pager, rows); err != nil {
		_ = pager.Close()
		return err
	}
	if err := pager.Close(); err != nil {
		return fmt.Errorf("close pager: %w", err)
	}

	if spec.FailOnAny && len(rows) > 0 {
		return fmt.Errorf("--fail: %d %s(s) matched the filter: %w",
			len(rows), spec.ResourceLabel, ErrMatched)
	}
	if allFailed {
		return fmt.Errorf("every configured backend failed to list %ss: %w",
			spec.ResourceLabel, ErrAllBackendsFailed)
	}
	return nil
}

// fanOut runs Spec.Fetcher per configured backend in parallel via
// errgroup and returns the concatenated rows, the "every backend
// failed" boolean (ADR 0009 lenient partial-failure rule), and the
// per-backend errors. Concurrency is genuine parallelism — backends
// are independent HTTP endpoints — but the caller of Run must not
// observe scheduler nondeterminism, hence the rows + stderr sort in
// Run after fanOut returns.
//
// Per-result state is captured into a slice indexed by backend
// position so the result aggregation does not need a per-goroutine
// channel send: each goroutine writes its own slot, no mutex needed.
func fanOut[R any](ctx context.Context, spec Spec[R], deps Deps) (rows []R, allFailed bool, errs []error) {
	backends := spec.Config.Backends
	if len(backends) == 0 {
		return nil, false, nil
	}

	type result struct {
		rows []R
		err  error
	}
	results := make([]result, len(backends))

	g, gctx := errgroup.WithContext(ctx)
	for i, be := range backends {
		g.Go(func() error {
			c, err := deps.BuildClient(be)
			if err != nil {
				results[i] = result{err: fmt.Errorf("backend %q: build: %w", be.Name, err)}
				return nil
			}
			r, err := spec.Fetcher(gctx, be.Name, c)
			if err != nil {
				results[i] = result{err: fmt.Errorf("backend %q: list: %w", be.Name, err)}
				return nil
			}
			results[i] = result{rows: r}
			return nil
		})
	}
	// errgroup.Wait never returns an error here because every goroutine
	// returns nil — per-backend errors are captured into results[i]
	// for the lenient partial-failure aggregation downstream.
	_ = g.Wait()

	failed := 0
	for _, res := range results {
		if res.err != nil {
			failed++
			errs = append(errs, res.err)
			continue
		}
		rows = append(rows, res.rows...)
	}
	return rows, failed == len(backends), errs
}

// defaultPagerFactory is the zero-value Deps.PagerFactory. Lives in
// listcmd as a function-typed seam the cmd layer overrides with a
// closure over cmd.NewPager — the package cannot import cmd without
// a cycle, so production wiring crosses the boundary by injection.
// The seam itself returns a write-through closer so direct callers
// (and unit tests that pass a Deps{}) still get a valid Pager.
func defaultPagerFactory(_ context.Context, fallback io.Writer, _, _ bool) (io.WriteCloser, error) {
	return writeThroughCloser{fallback}, nil
}

// writeThroughCloser is the no-op closer the default PagerFactory
// hands back. Mirrors cmd.Disabled's behaviour without importing
// cmd: Write forwards, Close is a no-op.
type writeThroughCloser struct{ io.Writer }

// Close implements io.Closer. The default factory never spawns a
// subprocess so there is nothing to wait on.
func (writeThroughCloser) Close() error { return nil }

// isTerminal reports whether w is os.Stdout AND connected to a real
// TTY. Mirrors cmd.isStdoutTerminal so the pipeline keeps the same
// table-vs-json default heuristic without crossing the cmd boundary.
// A test-injected bytes.Buffer (or any non-os.File writer) returns
// false and goes through the default-pipe path.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return output.IsTerminal(f)
}

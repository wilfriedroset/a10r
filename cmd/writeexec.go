// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/output"
)

// writeTarget is one silence mutation a write verb will perform: the
// tenant it lands in, the silence id (empty for create), the spec (zero
// for expire), and a pre-known failure that skips the RPC entirely
// (e.g. an expired silence that update refuses, reported without a
// pointless round-trip).
type writeTarget struct {
	tenant string
	id     string
	spec   backend.SilenceSpec
	skip   error
}

// writeOp performs one verb's backend mutation against a built client
// and returns the silence id to report (the new id for create/recreate,
// the unchanged id for update/expire).
type writeOp func(ctx context.Context, c backend.Client, t writeTarget) (string, error)

// writeHint builds the next-step hint (ADR 0045) for a verb's outcomes, or
// "" to emit none. It receives every result; the builders read only the
// successes.
type writeHint func(results []writeResult) string

// runWrites is the shared executor for every silence write verb: build
// at most one client per tenant, apply op to each target, and render the
// per-target outcomes under status (the success word). A target with a
// pre-known skip error is reported as a failure without an RPC. Errors
// are captured per target rather than aborting, so a partial failure
// still reports what landed and what did not. The fail-closed gate runs
// before this — by the time op fires, every target is known writable.
//
// After rendering, hint emits an undo/verify next-step to stderr (in every
// output mode) so a caller — human or agent — gets the reverse command for
// what just landed; stdout stays the pure result.
func runWrites(
	ctx context.Context,
	out, errOut io.Writer,
	cfg *config.Config,
	build listcmd.ClientFactory,
	format output.Format,
	status string,
	targets []writeTarget,
	hint writeHint,
	op writeOp,
) error {
	beByName := make(map[string]config.Backend, len(cfg.Backends))
	for _, be := range cfg.Backends {
		beByName[be.Name] = be
	}
	clients := make(map[string]backend.Client, len(cfg.Backends))
	clientFor := func(name string) (backend.Client, error) {
		if c, ok := clients[name]; ok {
			return c, nil
		}
		c, err := build(beByName[name])
		if err != nil {
			return nil, err
		}
		clients[name] = c
		return c, nil
	}

	// opErrs runs parallel to results: the typed error behind each op (or
	// build) failure, nil for a success or a pre-known skip. It lets the
	// exit classifier tell an all-unreachable write (retryable, exit 3)
	// from a generic one without re-parsing the stringified messages.
	results := make([]writeResult, 0, len(targets))
	opErrs := make([]error, 0, len(targets))
	for _, t := range targets {
		if t.skip != nil {
			results = append(results, writeResult{Tenant: t.tenant, ID: t.id, Status: writeStatusError, Error: t.skip.Error()})
			opErrs = append(opErrs, nil)
			continue
		}
		c, err := clientFor(t.tenant)
		if err != nil {
			results = append(results, writeResult{Tenant: t.tenant, ID: t.id, Status: writeStatusError, Error: err.Error()})
			opErrs = append(opErrs, err)
			continue
		}
		id, err := op(ctx, c, t)
		if err != nil {
			results = append(results, writeResult{Tenant: t.tenant, ID: t.id, Status: writeStatusError, Error: err.Error()})
			opErrs = append(opErrs, err)
			continue
		}
		results = append(results, writeResult{Tenant: t.tenant, ID: id, Status: status})
		opErrs = append(opErrs, nil)
	}
	err := emitWriteResults(out, errOut, format, results, opErrs)
	if hint != nil {
		if msg := hint(results); msg != "" {
			fmt.Fprintln(errOut, msg)
		}
	}
	return err
}

// targetTenants returns the distinct tenant names in targets, preserving
// first-seen order so the fail-closed writability error reads
// deterministically.
func targetTenants(targets []writeTarget) []string {
	seen := make(map[string]bool, len(targets))
	var out []string
	for _, t := range targets {
		if !seen[t.tenant] {
			seen[t.tenant] = true
			out = append(out, t.tenant)
		}
	}
	return out
}

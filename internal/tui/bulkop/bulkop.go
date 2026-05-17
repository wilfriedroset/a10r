// SPDX-License-Identifier: Apache-2.0

// Package bulkop holds the per-tenant fan-out machinery shared by
// the alerts page's bulk-silence flow and the silences page's
// bulk-expire flow. It is deliberately a two-caller dedup and lives
// outside internal/tui/page/listpage — the listpage package enforces
// a rule-of-three inclusion bar, and bulk is intentionally below
// that threshold. ADR 0013 documents the boundary.
//
// The package owns three responsibilities:
//
//  1. Group ops by tenant.
//  2. Run a per-tenant bounded worker pool with a caller-supplied
//     Writer closure (the write call differs per page: CreateSilence
//     for alerts, ExpireSilence for silences).
//  3. Aggregate results into a DoneMsg the page emits as a tea.Msg.
//
// Page-specific orchestration stays at the call site:
//
//   - modal/form flows (alerts goes alert -> form -> fan-out;
//     silences goes silence -> confirm -> fan-out)
//   - per-page pending state structs and resolution
//   - flash wording (verbs differ: "silenced N alerts" vs
//     "expired N silences")
//   - failure logging field names (alert_fingerprint vs silence_id)
//
// Generic over K to keep the page's key type (alert fingerprint vs
// silence ID) untyped-string-free at the page boundary.
package bulkop

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// ErrNoWriteableBackend is the sentinel a Writer returns when its
// captured client map has no entry for the op's tenant. Pages should
// detect their own no-client condition in the Writer closure and
// return this so the dispatcher counts the op as a failure without
// the call site having to invent its own error string.
var ErrNoWriteableBackend = errors.New("no writeable backend for tenant")

// Op is one keyed unit of work to dispatch against a single tenant.
// Key is whatever identifier the page uses to reconcile the result
// back onto its local state (an alert fingerprint, a silence ID,
// etc); Tenant routes the call to the right backend.
type Op[K comparable] struct {
	Key    K
	Tenant string
}

// Result is the outcome of one Op. Err==nil means success; Ack
// carries any server-returned acknowledgement token the writer
// chose to surface (e.g. a freshly-created silence ID) — empty for
// writers that don't have one.
type Result[K comparable] struct {
	Op  Op[K]
	Ack string
	Err error
}

// Writer performs the per-tenant write. The page passes a closure
// that has captured everything it needs (its client map, any spec
// boilerplate). The dispatcher invokes Writer concurrently across
// tenants and within a tenant's worker pool; closures must be safe
// for concurrent use.
type Writer[K comparable] func(ctx context.Context, tenant string, op Op[K]) (ack string, err error)

// DoneMsg is the tea.Msg the dispatcher emits when every op has
// either completed or been short-circuited by ctx cancellation.
// Results preserves one entry per op submitted to Dispatch — pages
// derive their own "successes" / "failures" subset by ranging over
// Results and filtering on Err.
type DoneMsg[K comparable] struct {
	Results []Result[K]
}

// Successes returns the keys of every Result whose Err is nil.
// Both call sites today want exactly this list to drive their
// per-success mark cleanup; lifted so each page doesn't repeat the
// range loop.
func (m DoneMsg[K]) Successes() []K {
	out := make([]K, 0, len(m.Results))
	for _, r := range m.Results {
		if r.Err == nil {
			out = append(out, r.Op.Key)
		}
	}
	return out
}

// Dispatch fans out ops grouped by tenant, applying writer per
// (tenant, op), and returns a tea.Cmd that emits DoneMsg[K] once
// every op has finished. concurrency caps the per-tenant worker
// pool at max(1, min(concurrency, len(tenantOps))). Tenants run in
// parallel — the cap is per-tenant, not global.
//
// Cancellation: ctx propagates into every Writer call. The producer
// goroutine that feeds each tenant's job channel respects ctx.Done
// so a mid-flight cancel stops dispatching new work; ops the workers
// have already pulled run to completion (idempotent on both call
// sites' verbs). Ops the producer drops on cancel land in Results
// with Err==ctx.Err so DoneMsg.Results has exactly one entry per
// submitted Op.
func Dispatch[K comparable](
	ctx context.Context,
	ops []Op[K],
	writer Writer[K],
	concurrency int,
) tea.Cmd {
	return func() tea.Msg {
		return DoneMsg[K]{Results: run(ctx, ops, writer, concurrency)}
	}
}

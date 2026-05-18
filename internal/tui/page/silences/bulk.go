// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// pendingExpire is the in-flight state between an opened expire
// confirm modal and its ConfirmResultMsg. ids is the set of
// silence IDs to expire on Yes, paired with the tenant the
// silence lives on (resolved at modal-open time so a poll-tick
// reordering or filter change between open and Yes never expires
// a different silence on a different backend). bulk picks the
// audit surface tag — single-row `x` from the cursor reports
// "expire-confirm", bulk-marked `x` reports "bulk-expire".
type pendingExpire struct {
	ids  []pendingExpireID
	bulk bool
}

// pendingExpireID pairs a silence ID with its tenant so the
// confirm-result handler can route ExpireSilence without
// re-reading the live view.
type pendingExpireID struct {
	id     string
	tenant string
}

// hintNoWriteableBackend is the shared message every write action
// flashes when no Client is reachable for the active scope.
// Mirrors the alerts / alert / groups pages so the affordance
// reads identically across resources.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

// openExpireConfirmUnified routes `x` to the single-row or bulk
// expire confirm depending on whether any silences are marked.
// Mirror of the alerts page's openSilenceForS.
func (p *Page) openExpireConfirmUnified() tea.Cmd {
	if len(p.marks) == 0 {
		return p.openExpireConfirm()
	}
	return p.openBulkExpireConfirm()
}

// openExpireConfirm opens the single-row expire confirm modal for
// the cursor row. No-op flashes when the cursor is past the view,
// when no backends are writeable, or when the cursor row's tenant
// is not in the writeable set. Pending state (id + tenant) is
// captured at modal-open time so a poll-tick reordering between
// open and Yes still routes ExpireSilence at the right backend.
func (p *Page) openExpireConfirm() tea.Cmd {
	if p.Index() >= len(p.view) {
		return flashFn(footer.FlashInfo, "no silence under the cursor")
	}
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	entry := p.view[p.Index()]
	if _, ok := p.clients[entry.tenant]; !ok {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	p.pendingExpire = pendingExpire{
		ids:  []pendingExpireID{{id: entry.s.ID, tenant: entry.tenant}},
		bulk: false,
	}
	question := "expire silence " + entry.s.ID + "?"
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultNo)
	})
}

// openBulkExpireConfirm queues every marked silence for bulk
// expiration. Walks p.byTenant (not p.view) so a marked silence
// hidden by an active filter still gets expired — the user
// marked it deliberately and a filter change shouldn't silently
// drop it from the queue. Empty marks → soft Info flash hinting
// at the Space binding so the user discovers the affordance.
//
// Question wording matches docs/design/bulk-silence.md: a single
// queued silence keeps the existing single-row "expire silence
// <id>?" wording (functionally identical to the cursor-row path);
// two-or-more uses "expire N silences? (tenant <breakdown>)" so
// the user can see at a glance how many backends the fanout will
// touch. Default-No because expire is mostly-irreversible — the
// next poll re-fires the alert and on-call may page.
func (p *Page) openBulkExpireConfirm() tea.Cmd {
	if len(p.marks) == 0 {
		return flashFn(footer.FlashInfo, "no rows marked — Space marks one")
	}
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	ids := make([]pendingExpireID, 0, len(p.marks))
	for tenant, sils := range p.byTenant {
		for _, s := range sils {
			if _, m := p.marks[s.ID]; m {
				ids = append(ids, pendingExpireID{id: s.ID, tenant: tenant})
			}
		}
	}
	if len(ids) == 0 {
		// Marks survived but every silence vanished from byTenant
		// (every backend re-emitted without them). Defensive:
		// flash and clear so the user can re-mark.
		return flashFn(footer.FlashInfo, "no marked silences remain")
	}
	// Sort by ID for deterministic confirm-question wording and
	// stable iteration order across runs / tests.
	sort.Slice(ids, func(i, j int) bool { return ids[i].id < ids[j].id })
	p.pendingExpire = pendingExpire{ids: ids, bulk: true}
	var question string
	if len(ids) == 1 {
		question = "expire silence " + ids[0].id + "?"
	} else {
		question = fmt.Sprintf("expire %d silences? (tenant %s)", len(ids), formatTenantBreakdown(ids))
	}
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultNo)
	})
}

// formatTenantBreakdown renders the per-tenant count for the
// bulk-expire confirm modal. Single tenant returns the bare name
// (`"prod"`); multi-tenant returns a comma-joined `name=count`
// sequence sorted alphabetically by tenant for stable wording
// across runs (`"prod=12, staging=3"`).
func formatTenantBreakdown(ids []pendingExpireID) string {
	counts := map[string]int{}
	tenants := []string{}
	for _, id := range ids {
		if _, seen := counts[id.tenant]; !seen {
			tenants = append(tenants, id.tenant)
		}
		counts[id.tenant]++
	}
	sort.Strings(tenants)
	if len(tenants) == 1 {
		return tenants[0]
	}
	parts := make([]string, len(tenants))
	for i, t := range tenants {
		parts[i] = fmt.Sprintf("%s=%d", t, counts[t])
	}
	return strings.Join(parts, ", ")
}

// bulkExpireDoneMsg is the page-local wrapper around the bulkop
// DoneMsg. The bulk bool rides along solely to pick the audit
// surface tag ("expire-confirm" vs "bulk-expire") — the flash
// wording is derived from totals alone. Wrapping (instead of
// using bulkop.DoneMsg[string] directly the way alerts does)
// keeps the surface-tag side channel page-local without polluting
// bulkop with caller-specific provenance.
type bulkExpireDoneMsg struct {
	bulk bool
	done bulkop.DoneMsg[string]
}

// handleExpireConfirm consumes a ConfirmResultMsg arriving after
// an expire confirm modal. Yes kicks off the bulk-expire fanout
// via bulkop.Dispatch; the resulting bulkExpireDoneMsg arrives on
// Update and applies the unmark + flash. No / Cancelled clears
// the pending state silently. Tenants are read directly from the
// captured pair — no live-view lookup — so a poll tick or filter
// change between Open and Yes never reroutes the expire to the
// wrong backend or drops it as "unknown".
//
// Cancellation: a fresh context.Context is created per round and
// stored in p.cancelBulk. Close() on the page calls it; workers
// see the cancellation via the worker-channel select and exit
// without processing remaining IDs. The Cmd defers its own
// cancel() so a completed round releases its ctx without the
// done-handler having to look at p.cancelBulk — that field always
// refers to the *latest* round, not the one whose done message we
// happen to be processing. Any in-flight ExpireSilence runs to
// completion — expire is idempotent on the AM side, so finishing
// a request mid-cancel doesn't risk double-effect.
func (p *Page) handleExpireConfirm(m modal.ConfirmResultMsg) tea.Cmd {
	pending := p.pendingExpire
	p.pendingExpire = pendingExpire{}
	if m.Cancelled || !m.Yes || len(pending.ids) == 0 {
		return nil
	}
	if p.cancelBulk != nil {
		// A second confirm landing while a prior fanout hasn't
		// drained replaces its context. The prior round's in-flight
		// workers see Done and skip the rest; its own deferred
		// cancel() is then a no-op (idempotent).
		p.cancelBulk()
	}
	parent := p.bulkCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancelBulk = cancel
	clients := p.clients
	bulk := pending.bulk
	ops := make([]bulkop.Op[string], 0, len(pending.ids))
	for _, id := range pending.ids {
		ops = append(ops, bulkop.Op[string]{Key: id.id, Tenant: id.tenant})
	}
	writer := func(ctx context.Context, tenant string, op bulkop.Op[string]) (string, error) {
		c, ok := clients[tenant]
		if !ok {
			return "", bulkop.ErrNoWriteableBackend
		}
		return "", c.ExpireSilence(ctx, op.Key)
	}
	dispatch := bulkop.Dispatch(ctx, ops, writer, p.bulkConcurrency)
	return func() tea.Msg {
		// Local cancel: releases this round's ctx subtree the moment
		// dispatch returns, regardless of whether p.cancelBulk has
		// since been overwritten by a newer round.
		defer cancel()
		done, _ := dispatch().(bulkop.DoneMsg[string])
		return bulkExpireDoneMsg{bulk: bulk, done: done}
	}
}

// handleBulkExpireDone applies a completed bulk-expire fanout.
// Successes drop their marks; everything else (failures and
// unstarted-due-cancel) keeps its mark so re-pressing `x` retries
// only the unfinished work. The flash summary distinguishes
// all-success / partial / all-fail wording.
//
// Does not touch p.cancelBulk — that field may now point to a
// newer round's cancel func (the user re-fired `x` while this
// fanout was still draining). The Cmd that produced this message
// already deferred its own cancel(), so the local ctx is released
// without the handler having to disambiguate.
//
// Per-failure attribution is emitted here (not in bulkop) because
// the slog field name is page-specific (silence_id).
func (p *Page) handleBulkExpireDone(m bulkExpireDoneMsg) tea.Cmd {
	source := "expire-confirm"
	if m.bulk {
		source = "bulk-expire"
	}
	total := len(m.done.Results)
	successes := 0
	for _, r := range m.done.Results {
		if r.Err == nil {
			delete(p.marks, r.Op.Key)
			auditSilenceWrite("expired", r.Op.Key, source)
			successes++
			continue
		}
		if p.logger != nil {
			p.logger.Error("bulk expire: silence expire failed",
				slog.String("backend", r.Op.Tenant),
				slog.String("tenant", r.Op.Tenant),
				slog.String("silence_id", r.Op.Key),
				slog.String("err", r.Err.Error()),
			)
		}
	}
	return p.flashExpireResult(m.bulk, successes, total-successes)
}

// flashExpireResult picks the flash level (success / warn / error)
// and message wording from the success/failure counts of an expire
// fanout. The leading bool arg is unused today; kept to mirror the
// alerts page's bulk-result helper signature.
func (p *Page) flashExpireResult(_ bool, success, failed int) tea.Cmd {
	total := success + failed
	if total == 1 {
		if success == 1 {
			return flashFn(footer.FlashSuccess, "silence expired")
		}
		return flashFn(footer.FlashError, "expire failed")
	}
	if failed == 0 {
		return flashFn(footer.FlashSuccess, fmt.Sprintf("expired %d silences", success))
	}
	if success == 0 {
		return flashFn(footer.FlashError, fmt.Sprintf("expire failed for %d silences", failed))
	}
	return flashFn(footer.FlashWarn, fmt.Sprintf("expired %d of %d — %d failed", success, total, failed))
}

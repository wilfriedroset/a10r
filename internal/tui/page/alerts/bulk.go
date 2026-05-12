// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

type bulkSilenceTarget struct {
	Tenant      string
	Fingerprint string
	Matchers    []backend.Matcher
}

// pendingBulkSilence captures the in-flight state between the
// confirm modal (or bulk-form push) and its result. Empty between
// rounds. Targets is the resolved list of {tenant, fingerprint,
// matchers}; tenants is a stable alphabetical list of distinct
// tenant names for the confirm question and the form banner.
type pendingBulkSilence struct {
	targets []bulkSilenceTarget
	tenants []string
}

// openBulkSilence resolves the marked alerts into bulkSilenceTargets
// (matchers minus `__name__`, paired with each alert's tenant) and
// either pushes the bulk form directly (N=1) or opens a confirm
// modal first (N≥2). Marks that no longer correspond to any in-
// scope alert (e.g. the alert resolved between mark and silence)
// are dropped silently. Empty Clients flashes the standard hint;
// no marks left after resolution drops to a soft Info flash.
func (p *Page) openBulkSilence() tea.Cmd {
	if len(p.clients) == 0 {
		return flashFn(footer.FlashWarn, hintNoWriteableBackend)
	}
	targets, tenants := p.resolveBulkSilenceTargets()
	if len(targets) == 0 {
		return flashFn(footer.FlashInfo, "no marked alerts remain")
	}
	p.pendingBulkSilence = pendingBulkSilence{targets: targets, tenants: tenants}
	if len(targets) == 1 {
		return p.pushBulkSilenceForm()
	}
	question := fmt.Sprintf("silence %d alerts? (tenant %s)", len(targets), formatTenantBreakdownAlerts(targets))
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(question, modal.ConfirmDefaultYes)
	})
}

// resolveBulkSilenceTargets walks p.byTenant (not p.view) so a
// marked alert hidden by an active filter still ends up in the
// queue — the user marked it deliberately, an unrelated UI state
// shouldn't silently drop it. Targets are sorted by (tenant,
// fingerprint) so the confirm wording and the fanout order are
// stable across runs / tests. Returns the resolved list plus a
// stable alphabetical list of distinct tenant names for the
// confirm question + form banner.
func (p *Page) resolveBulkSilenceTargets() (targets []bulkSilenceTarget, tenants []string) {
	targets = make([]bulkSilenceTarget, 0, len(p.marks))
	tenantSet := map[string]struct{}{}
	for tenant, alerts := range p.byTenant {
		if _, ok := p.clients[tenant]; !ok {
			continue
		}
		for _, a := range alerts {
			if _, marked := p.marks[a.Fingerprint]; !marked {
				continue
			}
			targets = append(targets, bulkSilenceTarget{
				Tenant:      tenant,
				Fingerprint: a.Fingerprint,
				Matchers:    silenceform.MatchersFromLabels(a.Labels),
			})
			tenantSet[tenant] = struct{}{}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Tenant != targets[j].Tenant {
			return targets[i].Tenant < targets[j].Tenant
		}
		return targets[i].Fingerprint < targets[j].Fingerprint
	})
	tenants = make([]string, 0, len(tenantSet))
	for t := range tenantSet {
		tenants = append(tenants, t)
	}
	sort.Strings(tenants)
	return targets, tenants
}

// formatTenantBreakdownAlerts mirrors the silences page's
// formatTenantBreakdown shape but counts targets-per-tenant on a
// []bulkSilenceTarget rather than []pendingExpireID. Single-tenant
// returns the bare name; multi-tenant returns "name=count" pairs
// sorted alphabetically and joined with ", ".
func formatTenantBreakdownAlerts(targets []bulkSilenceTarget) string {
	counts := map[string]int{}
	tenants := []string{}
	for _, t := range targets {
		if _, seen := counts[t.Tenant]; !seen {
			tenants = append(tenants, t.Tenant)
		}
		counts[t.Tenant]++
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

// pushBulkSilenceForm pushes the silence form in bulk mode with
// a banner spelling out the per-target fanout shape. Uses the
// pending state populated by openBulkSilence — caller must have
// validated client availability already. The whole p.clients map
// is forwarded for symmetry with the single-form path; in bulk
// mode the form never resolves a Client, so the map is informational
// — but per ADR-0011 the form's API now takes Clients in every
// path, so the bulk caller threads it through unchanged.
//
// The 1-mark path renders this banner ("applies to 1 alert
// (tenant prod)") rather than the cursor row's matchers buffer
// the no-marks path uses. Two single-target paths render and
// confirm differently on purpose: the bulk path can't show a
// single backend ID up front (the silence is created post-submit)
// and the form's banner is the user's gate at N=1. Don't try to
// "unify" them — the divergence is per-design.
func (p *Page) pushBulkSilenceForm() tea.Cmd {
	pending := p.pendingBulkSilence
	if len(pending.targets) == 0 {
		return flashFn(footer.FlashInfo, "no marked alerts remain")
	}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	banner := bulkSilenceBanner(pending.targets, pending.tenants)
	clients := p.clients
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:    clients,
			Styles:     styles,
			Now:        now,
			Creator:    creator,
			Bulk:       true,
			BulkBanner: banner,
		})
	})
}

// bulkSilenceBanner formats the form's banner string. Single-
// tenant + N=1 reads "applies to 1 alert (tenant prod)";
// otherwise "applies to N alerts across M tenants — each
// silenced with its own labels". Wording matches docs/design/
// bulk-silence.md so the user sees exactly what the submit will
// fan out to.
func bulkSilenceBanner(targets []bulkSilenceTarget, tenants []string) string {
	n := len(targets)
	if len(tenants) == 1 {
		alertWord := "alerts"
		if n == 1 {
			alertWord = "alert"
		}
		return fmt.Sprintf("applies to %d %s (tenant %s)", n, alertWord, tenants[0])
	}
	return fmt.Sprintf("applies to %d alerts across %d tenants — each silenced with its own labels", n, len(tenants))
}

// handleBulkSilenceConfirm consumes a ConfirmResultMsg from the
// pre-form confirm modal (N≥2 path). Yes pushes the bulk form;
// No / Cancelled drops the pending state silently. The single-
// row confirm also lands here when openExpireConfirmUnified-
// shaped flows ever need it on the alerts page; today there are
// no such, so the absence of pending state is a plain no-op.
func (p *Page) handleBulkSilenceConfirm(m modal.ConfirmResultMsg) tea.Cmd {
	pending := p.pendingBulkSilence
	if len(pending.targets) == 0 {
		return nil
	}
	if m.Cancelled || !m.Yes {
		p.pendingBulkSilence = pendingBulkSilence{}
		return nil
	}
	return p.pushBulkSilenceForm()
}

// bulkSilenceDoneMsg is the result envelope for a completed
// bulk-silence fanout. Successes carries the alert fingerprints
// whose CreateSilence returned nil — Update unmarks those rows;
// fingerprints absent from the list (failures or unstarted-due-
// cancel) keep their marks so retry is one keystroke. Total is
// the original target count so the flash can read "silenced N of
// Total".
type bulkSilenceDoneMsg struct {
	total     int
	successes []string
}

// handleBulkSilenceSubmit runs after the bulk form auto-pops on
// Ctrl+S submit. The user has filled the metadata (comment,
// starts/ends, creator) once; the page stamps it onto every
// pending target's matcher set and dispatches the fanout. The
// returned Cmd performs the worker-pool dispatch and emits
// bulkSilenceDoneMsg when every result has landed.
func (p *Page) handleBulkSilenceSubmit(m silenceform.BulkSubmittedMsg) tea.Cmd {
	pending := p.pendingBulkSilence
	p.pendingBulkSilence = pendingBulkSilence{}
	if len(pending.targets) == 0 {
		return nil
	}
	if p.cancelBulk != nil {
		// Cancel any prior in-flight round before starting a new one.
		// Idempotent: the prior round's deferred cancel() is a no-op
		// if it already ran.
		p.cancelBulk()
	}
	parent := p.bulkCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancelBulk = cancel
	clients := p.clients
	concurrency := p.bulkConcurrency
	logger := p.logger
	targets := pending.targets
	spec := backend.SilenceSpec{
		StartsAt:  m.StartsAt,
		EndsAt:    m.EndsAt,
		CreatedBy: m.Creator,
		Comment:   m.Comment,
	}
	return func() tea.Msg {
		// Local cancel: releases this round's ctx subtree the moment
		// dispatch returns, regardless of whether p.cancelBulk has
		// since been overwritten by a newer round.
		defer cancel()
		successes := dispatchBulkSilence(ctx, clients, targets, spec, concurrency, logger)
		return bulkSilenceDoneMsg{
			total:     len(targets),
			successes: successes,
		}
	}
}

// handleBulkSilenceDone applies a completed bulk-silence fanout.
// Successes drop their marks; everything else (failures and
// unstarted-due-cancel) keeps its mark. Does not touch p.cancelBulk
// — that field may now refer to a newer round; the producing Cmd
// already deferred its own cancel().
func (p *Page) handleBulkSilenceDone(m bulkSilenceDoneMsg) tea.Cmd {
	for _, fp := range m.successes {
		delete(p.marks, fp)
		// successes carries alert fingerprints (the bulk fanout
		// emits one CreateSilence per alert; the resulting silence
		// IDs are not propagated back). The audit log uses the
		// fingerprint as the identifier so reconstruction can
		// correlate against the alerts list / backend snapshot.
		slog.Default().Info("silence write succeeded",
			slog.String("op", "created"),
			slog.String("alert_fingerprint", fp),
			slog.String("surface", "bulk-silence"))
	}
	failed := m.total - len(m.successes)
	return flashBulkSilenceResult(m.total, len(m.successes), failed)
}

// flashBulkSilenceResult formats the success / partial / total-
// failure flash text for a completed bulk-silence round. N=1 reads
// "silence created" (matching the single-row form's success flash);
// N≥2 uses count-based wording.
func flashBulkSilenceResult(total, success, failed int) tea.Cmd {
	if total == 1 {
		if success == 1 {
			return flashFn(footer.FlashSuccess, "silence created")
		}
		return flashFn(footer.FlashError, "silence failed")
	}
	if failed == 0 {
		return flashFn(footer.FlashSuccess, fmt.Sprintf("silenced %d alerts", success))
	}
	if success == 0 {
		return flashFn(footer.FlashError, fmt.Sprintf("silence failed for %d alerts", failed))
	}
	return flashFn(footer.FlashWarn, fmt.Sprintf("silenced %d of %d — %d failed", success, total, failed))
}

// silenceResult is the per-call outcome the worker pool emits
// onto the shared results channel. Tenant rides along for
// structured-log attribution on failure.
type silenceResult struct {
	fingerprint string
	tenant      string
	err         error
}

// dispatchBulkSilence runs the per-tenant fanout. Tenants run in
// parallel goroutines; inside each tenant a bounded worker pool
// of `min(concurrency, len(targets))` workers consumes from a
// per-tenant jobs channel. concurrency=1 collapses to fully
// sequential per tenant. Mirrors the silences page's
// dispatchBulkExpire shape — the only differences are the verb
// (CreateSilence vs ExpireSilence) and the result shape.
//
// Returns the alert fingerprints whose CreateSilence returned
// nil. The caller derives "failed = total - len(successes)"; that
// bucket includes both real errors and unstarted-due-cancel. Both
// keep their marks so the user can retry only the unfinished work.
func dispatchBulkSilence(
	ctx context.Context,
	clients map[string]silenceform.Client,
	targets []bulkSilenceTarget,
	specBase backend.SilenceSpec,
	concurrency int,
	logger *slog.Logger,
) []string {
	byTenant := map[string][]bulkSilenceTarget{}
	tenants := []string{}
	for _, t := range targets {
		if _, seen := byTenant[t.Tenant]; !seen {
			tenants = append(tenants, t.Tenant)
		}
		byTenant[t.Tenant] = append(byTenant[t.Tenant], t)
	}
	resCh := make(chan silenceResult, len(targets))
	var tenantWg sync.WaitGroup
	for _, tenant := range tenants {
		client, ok := clients[tenant]
		group := byTenant[tenant]
		if !ok {
			for _, t := range group {
				resCh <- silenceResult{
					fingerprint: t.Fingerprint,
					tenant:      tenant,
					err:         errors.New("no writeable backend for tenant"),
				}
			}
			continue
		}
		tenantWg.Add(1)
		go func(tenant string, ts []bulkSilenceTarget, c silenceform.Client) {
			defer tenantWg.Done()
			runTenantSilencePool(ctx, tenant, ts, c, specBase, concurrency, resCh)
		}(tenant, group, client)
	}
	go func() {
		tenantWg.Wait()
		close(resCh)
	}()
	successes := make([]string, 0, len(targets))
	for r := range resCh {
		if r.err == nil {
			successes = append(successes, r.fingerprint)
			continue
		}
		if logger != nil {
			logger.Error("bulk silence: alert silence failed",
				slog.String("backend", r.tenant),
				slog.String("tenant", r.tenant),
				slog.String("alert_fingerprint", r.fingerprint),
				slog.String("err", r.err.Error()),
			)
		}
	}
	return successes
}

// runTenantSilencePool is the per-tenant bounded worker pool.
// Producer feeds the jobs channel under ctx.Done so a Close()
// mid-flight stops dispatching new work; consumers run
// CreateSilence and emit results regardless of the ctx state for
// jobs they've already pulled, so an in-flight request completes
// naturally. Workers cap at min(concurrency, len(targets)).
func runTenantSilencePool(
	ctx context.Context,
	tenant string,
	targets []bulkSilenceTarget,
	client silenceform.Client,
	specBase backend.SilenceSpec,
	concurrency int,
	resCh chan<- silenceResult,
) {
	workers := max(min(concurrency, len(targets)), 1)
	jobs := make(chan bulkSilenceTarget)
	go func() {
		defer close(jobs)
		for _, t := range targets {
			select {
			case <-ctx.Done():
				return
			case jobs <- t:
			}
		}
	}()
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for t := range jobs {
				spec := specBase
				spec.Matchers = t.Matchers
				_, err := client.CreateSilence(ctx, spec)
				resCh <- silenceResult{fingerprint: t.Fingerprint, tenant: tenant, err: err}
			}
		})
	}
	wg.Wait()
}

// hintNoWriteableBackend is the shared "configure a writeable
// backend" message every page flashes when `s` lands but no
// silenceform.Client is available. Pulled to a const so a wording
// change touches one site.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

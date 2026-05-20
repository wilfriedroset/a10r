// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
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
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
	}
	targets, tenants := p.resolveBulkSilenceTargets()
	if len(targets) == 0 {
		return footer.ShowFlash(footer.FlashInfo, "no marked alerts remain")
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
		return footer.ShowFlash(footer.FlashInfo, "no marked alerts remain")
	}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	banner := bulkSilenceBanner(pending.targets, pending.tenants)
	clients := p.clients
	submitCtx := p.submitCtx
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:    clients,
			Styles:     styles,
			Now:        now,
			Creator:    creator,
			Bulk:       true,
			BulkBanner: banner,
			SubmitCtx:  submitCtx,
		})
	})
}

// bulkSilenceBanner formats the form's banner string. Single-
// tenant + N=1 reads "applies to 1 alert (tenant prod)";
// otherwise "applies to N alerts across M tenants — each
// silenced with its own labels". The wording is deliberate: the
// user must see at a glance how many alerts and tenants the
// submit fans out across.
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

// handleBulkSilenceSubmit runs after the bulk form auto-pops on
// Ctrl+S submit. The user has filled the metadata (comment,
// starts/ends, creator) once; the page stamps it onto every
// pending target's matcher set and dispatches the fanout via
// bulkop.Dispatch. The returned Cmd emits bulkop.DoneMsg[string]
// once every CreateSilence has either landed or been short-
// circuited by ctx cancellation.
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
	specBase := backend.SilenceSpec{
		StartsAt:  m.StartsAt,
		EndsAt:    m.EndsAt,
		CreatedBy: m.Creator,
		Comment:   m.Comment,
	}
	matchersByFP := map[string][]backend.Matcher{}
	ops := make([]bulkop.Op[string], 0, len(pending.targets))
	for _, t := range pending.targets {
		matchersByFP[t.Fingerprint] = t.Matchers
		ops = append(ops, bulkop.Op[string]{Key: t.Fingerprint, Tenant: t.Tenant})
	}
	writer := func(ctx context.Context, tenant string, op bulkop.Op[string]) (string, error) {
		c, ok := clients[tenant]
		if !ok {
			return "", bulkop.ErrNoWriteableBackend
		}
		spec := specBase
		spec.Matchers = matchersByFP[op.Key]
		return c.CreateSilence(ctx, spec)
	}
	dispatch := bulkop.Dispatch(ctx, ops, writer, p.bulkConcurrency)
	return func() tea.Msg {
		// Local cancel: releases this round's ctx subtree the moment
		// dispatch returns, regardless of whether p.cancelBulk has
		// since been overwritten by a newer round.
		defer cancel()
		return dispatch()
	}
}

// handleBulkSilenceDone applies a completed bulk-silence fanout.
// Successes drop their marks; everything else (failures and
// unstarted-due-cancel) keeps its mark. Does not touch p.cancelBulk
// — that field may now refer to a newer round; the producing Cmd
// already deferred its own cancel().
//
// Per-failure attribution is emitted here (not in bulkop) because
// the slog field names are page-specific: alerts logs
// alert_fingerprint, silences logs silence_id.
func (p *Page) handleBulkSilenceDone(m bulkop.DoneMsg[string]) tea.Cmd {
	total := len(m.Results)
	successes := 0
	for _, r := range m.Results {
		if r.Err == nil {
			delete(p.marks, r.Op.Key)
			// Op.Key is the alert fingerprint (the bulk fanout emits
			// one CreateSilence per alert; the resulting silence IDs
			// surface on Result.Ack but the audit log keeps the
			// fingerprint as the identifier so reconstruction can
			// correlate against the alerts list / backend snapshot).
			slog.Default().Info("silence write succeeded",
				slog.String("op", "created"),
				slog.String("alert_fingerprint", r.Op.Key),
				slog.String("surface", "bulk-silence"))
			successes++
			continue
		}
		if p.logger != nil {
			p.logger.Error("bulk silence: alert silence failed",
				slog.String("backend", r.Op.Tenant),
				slog.String("tenant", r.Op.Tenant),
				slog.String("alert_fingerprint", r.Op.Key),
				slog.String("err", r.Err.Error()),
			)
		}
	}
	return flashBulkSilenceResult(total, successes, total-successes)
}

// flashBulkSilenceResult formats the success / partial / total-
// failure flash text for a completed bulk-silence round. N=1 reads
// "silence created" (matching the single-row form's success flash);
// N≥2 uses count-based wording.
func flashBulkSilenceResult(total, success, failed int) tea.Cmd {
	if total == 1 {
		if success == 1 {
			return footer.ShowFlash(footer.FlashSuccess, "silence created")
		}
		return footer.ShowFlash(footer.FlashError, "silence failed")
	}
	if failed == 0 {
		return footer.ShowFlash(footer.FlashSuccess, fmt.Sprintf("silenced %d alerts", success))
	}
	if success == 0 {
		return footer.ShowFlash(footer.FlashError, fmt.Sprintf("silence failed for %d alerts", failed))
	}
	return footer.ShowFlash(footer.FlashWarn, fmt.Sprintf("silenced %d of %d — %d failed", success, total, failed))
}

// hintNoWriteableBackend is the shared "configure a writeable
// backend" message every page flashes when `s` lands but no
// silenceform.Client is available. Pulled to a const so a wording
// change touches one site.
const hintNoWriteableBackend = "no writeable backend in scope — pick a tenant with `<1>`-`<9>` or `Ctrl+T`"

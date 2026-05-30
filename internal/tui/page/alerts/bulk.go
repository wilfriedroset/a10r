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
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

// pendingSilenceAll captures the single-cursor silence-all target
// (count>1) between its blast-radius confirm modal and the result.
// Empty between rounds. The matcher is always `alertname=<alertName>`
// — the aggregate's identity — so only the tenant / name / scope-note
// need carrying.
type pendingSilenceAll struct {
	tenant    string
	alertName string
	scopeNote string
}

// alertnameMatcher returns the single equality matcher that defines a
// group's identity. Silence-all (cursor and bulk) prefills this alone,
// NOT the full label set — the alertname aggregate's identity is the
// alertname (CONTEXT.md "Silence-all").
func alertnameMatcher(alertName string) []backend.Matcher {
	return []backend.Matcher{{Name: "alertname", Value: alertName, IsEqual: true}}
}

// silenceAllScopeNote states the true scope of a silence-all and, when
// the view is filtered, warns the filter is NOT applied to the
// prefilled matcher (the matcher is `alertname=X` regardless of any
// narrowing). No active filter → the bare scope line; filter and/or
// state filter active → the warning suffix naming the active filter.
func (p *Page) silenceAllScopeNote(g alertGroup) string {
	base := fmt.Sprintf("Silencing ALL instances of alertname=%s", g.alertName)
	if desc := p.activeFilterDesc(); desc != "" {
		base += fmt.Sprintf(" — the active filter (%s) is NOT applied", desc)
	}
	return base
}

// activeFilterDesc summarises the active substring / state filters for
// the scope note. Empty when neither is set. Both set → joined with a
// comma so the note names every narrowing in play.
func (p *Page) activeFilterDesc() string {
	var parts []string
	if p.Filter != "" {
		parts = append(parts, "filter "+p.Filter)
	}
	if p.stateFilter != "" {
		parts = append(parts, "state "+p.stateFilter)
	}
	return strings.Join(parts, ", ")
}

// silenceAllQuestion is the blast-radius confirm prompt for a
// single-cursor silence-all of a COUNT>1 group — the gate is the
// instance count, not a mark count.
func silenceAllQuestion(g alertGroup) string {
	return fmt.Sprintf("silence all %d instances of alertname=%s? (tenant %s)", g.count, g.alertName, g.tenant)
}

// pushSilenceAllForm pushes the silence form prefilled with the
// pending group's `alertname=X` matcher and its scope note. Caller has
// already validated client availability.
func (p *Page) pushSilenceAllForm() tea.Cmd {
	pending := p.pendingSilenceAll
	p.pendingSilenceAll = pendingSilenceAll{}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	tenant := pending.tenant
	matchers := alertnameMatcher(pending.alertName)
	scopeNote := pending.scopeNote
	submitCtx := p.submitCtx
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:   clients,
			Tenant:    tenant,
			Styles:    styles,
			Now:       now,
			Creator:   creator,
			Matchers:  matchers,
			ScopeNote: scopeNote,
			SubmitCtx: submitCtx,
		})
	})
}

// bulkSilenceTarget is one marked group's silence-all work: the group
// key (the bulkop key, also the mark key), the tenant the fanout
// resolves a Client for, and the `alertname=X` matcher.
type bulkSilenceTarget struct {
	Key       string
	Tenant    string
	AlertName string
	Matchers  []backend.Matcher
}

// pendingBulkSilence captures the resolved bulk silence-all targets
// between the confirm modal (N≥2) / bulk-form push and its result.
// Empty between rounds. tenants is a stable alphabetical list of
// distinct tenant names for the confirm question and the form banner.
type pendingBulkSilence struct {
	targets []bulkSilenceTarget
	tenants []string
}

// openBulkSilence resolves the marked groups into bulkSilenceTargets
// (one `alertname=X` silence per marked group, paired with its tenant)
// and either pushes the bulk form directly (N=1) or opens a confirm
// modal first (N≥2). Marks that no longer correspond to any in-scope
// group are dropped silently. Empty Clients flashes the standard hint;
// no marks left after resolution drops to a soft Info flash.
func (p *Page) openBulkSilence() tea.Cmd {
	if len(p.clients) == 0 {
		return footer.ShowFlash(footer.FlashWarn, listpage.HintNoWriteableBackend)
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

// resolveBulkSilenceTargets walks the current groups so a marked group
// hidden by an active filter is dropped only when the filter removed
// every instance (the group no longer exists). One target per marked
// group, keyed by the group key, with the `alertname=X` matcher.
// Targets are sorted by (tenant, alertName) so the confirm wording and
// fanout order are stable across runs / tests. Returns the resolved
// list plus a stable alphabetical list of distinct tenant names.
func (p *Page) resolveBulkSilenceTargets() (targets []bulkSilenceTarget, tenants []string) {
	targets = make([]bulkSilenceTarget, 0, len(p.marks))
	tenantSet := map[string]struct{}{}
	for _, g := range p.groups {
		if _, marked := p.marks[g.key()]; !marked {
			continue
		}
		if _, ok := p.clients[g.tenant]; !ok {
			continue
		}
		targets = append(targets, bulkSilenceTarget{
			Key:       g.key(),
			Tenant:    g.tenant,
			AlertName: g.alertName,
			Matchers:  alertnameMatcher(g.alertName),
		})
		tenantSet[g.tenant] = struct{}{}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Tenant != targets[j].Tenant {
			return targets[i].Tenant < targets[j].Tenant
		}
		return targets[i].AlertName < targets[j].AlertName
	})
	tenants = make([]string, 0, len(tenantSet))
	for t := range tenantSet {
		tenants = append(tenants, t)
	}
	sort.Strings(tenants)
	return targets, tenants
}

func formatTenantBreakdownAlerts(targets []bulkSilenceTarget) string {
	return bulkop.FormatTenantBreakdown(targets, func(t bulkSilenceTarget) string { return t.Tenant })
}

// pushBulkSilenceForm pushes the silence form in bulk mode with a
// banner spelling out the per-target fanout shape. Uses the pending
// state populated by openBulkSilence — caller must have validated
// client availability. The whole p.clients map is forwarded for
// symmetry with the single-form path; in bulk mode the form never
// resolves a Client.
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

// bulkSilenceBanner formats the form's banner. Single tenant reads
// "applies to N alerts (tenant prod) — one alertname silence each";
// multi-tenant names the tenant count. Each target is one
// `alertname=X` silence-all, so the wording stresses the per-alertname
// fanout (distinct from the L2 silence-one full-label fanout).
func bulkSilenceBanner(targets []bulkSilenceTarget, tenants []string) string {
	n := len(targets)
	word := "alerts"
	if n == 1 {
		word = "alert"
	}
	if len(tenants) == 1 {
		return fmt.Sprintf("applies to %d %s (tenant %s) — one alertname silence each", n, word, tenants[0])
	}
	return fmt.Sprintf("applies to %d alerts across %d tenants — one alertname silence each", n, len(tenants))
}

// handleConfirmResult routes a ConfirmResultMsg to whichever round is
// pending — the single-cursor silence-all (count>1) or the ≥2-marks
// bulk silence-all. The two are distinct paths with separate pending
// state; only one is ever set when a confirm result arrives.
func (p *Page) handleConfirmResult(m modal.ConfirmResultMsg) tea.Cmd {
	if p.pendingSilenceAll != (pendingSilenceAll{}) {
		return p.handleSilenceAllConfirm(m)
	}
	return p.handleBulkSilenceConfirm(m)
}

// handleSilenceAllConfirm consumes the single-cursor silence-all
// blast-radius confirm. Yes pushes the prefilled form; No / Cancelled
// drops the pending target.
func (p *Page) handleSilenceAllConfirm(m modal.ConfirmResultMsg) tea.Cmd {
	if p.pendingSilenceAll == (pendingSilenceAll{}) {
		return nil
	}
	if m.Cancelled || !m.Yes {
		p.pendingSilenceAll = pendingSilenceAll{}
		return nil
	}
	return p.pushSilenceAllForm()
}

// handleBulkSilenceConfirm consumes a ConfirmResultMsg from the
// pre-form bulk confirm modal (N≥2 path). Yes pushes the bulk form;
// No / Cancelled drops the pending state silently. An incoming message
// with no pending state is a plain no-op.
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

// handleBulkSilenceSubmit runs after the bulk form auto-pops on Ctrl+S
// submit. The user has filled the metadata once; the page stamps it
// onto every pending target's `alertname=X` matcher set and dispatches
// the fanout via bulkop.Dispatch — one CreateSilence per marked group.
func (p *Page) handleBulkSilenceSubmit(m silenceform.BulkSubmittedMsg) tea.Cmd {
	pending := p.pendingBulkSilence
	p.pendingBulkSilence = pendingBulkSilence{}
	if len(pending.targets) == 0 {
		return nil
	}
	if p.cancelBulk != nil {
		// Cancel any prior in-flight round before starting a new one.
		// Idempotent: the prior round's deferred cancel() is a no-op if
		// it already ran.
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
	matchersByKey := map[string][]backend.Matcher{}
	ops := make([]bulkop.Op[string], 0, len(pending.targets))
	for _, t := range pending.targets {
		matchersByKey[t.Key] = t.Matchers
		ops = append(ops, bulkop.Op[string]{Key: t.Key, Tenant: t.Tenant})
	}
	writer := func(ctx context.Context, tenant string, op bulkop.Op[string]) (string, error) {
		c, ok := clients[tenant]
		if !ok {
			return "", bulkop.ErrNoWriteableBackend
		}
		spec := specBase
		spec.Matchers = matchersByKey[op.Key]
		return c.CreateSilence(ctx, spec)
	}
	dispatch := bulkop.Dispatch(ctx, ops, writer, p.bulkConcurrency)
	return func() tea.Msg {
		// Local cancel: releases this round's ctx subtree the moment
		// dispatch returns, regardless of whether p.cancelBulk has since
		// been overwritten by a newer round.
		defer cancel()
		return dispatch()
	}
}

// handleBulkSilenceDone applies a completed bulk silence-all fanout.
// Successes drop their marks; everything else (failures and unstarted-
// due-cancel) keeps its mark so the next `s` retries only the
// unfinished work. Does not touch p.cancelBulk — that field may now
// refer to a newer round; the producing Cmd already deferred its own
// cancel().
func (p *Page) handleBulkSilenceDone(m bulkop.DoneMsg[string]) tea.Cmd {
	total := len(m.Results)
	successes := 0
	for _, r := range m.Results {
		if r.Err == nil {
			delete(p.marks, r.Op.Key)
			// Op.Key is the group key (tenant\x00alertname); the fanout
			// emits one alertname silence per marked group.
			slog.Default().Info("silence write succeeded",
				slog.String("op", "created"),
				slog.String("group_key", r.Op.Key),
				slog.String("surface", "bulk-silence"))
			successes++
			continue
		}
		if p.logger != nil {
			p.logger.Error("bulk silence: alert silence failed",
				slog.String("backend", r.Op.Tenant),
				slog.String("tenant", r.Op.Tenant),
				slog.String("group_key", r.Op.Key),
				slog.String("err", r.Err.Error()),
			)
		}
	}
	return flashBulkSilenceResult(total, successes, total-successes)
}

// flashBulkSilenceResult formats the success / partial / total-failure
// flash text for a completed bulk silence-all round. N=1 reads
// "silence created" (matching the single-form success flash); N≥2 uses
// count-based wording.
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

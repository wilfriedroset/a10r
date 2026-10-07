// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/guardrail"
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
	// confirmed records that the blast-radius modal already collected
	// the answer a guardrail rule asks for, so the form it pushes does
	// not ask the same tenant again for the same write.
	confirmed bool
}

// alertnameMatcher returns the single equality matcher that defines a
// group's identity. Silence-all (cursor and bulk) prefills this alone,
// NOT the full label set — the alertname aggregate's identity is the
// alertname (CONTEXT.md "Silence-all").
func alertnameMatcher(alertName string) []backend.Matcher {
	return []backend.Matcher{{Name: labelAlertname, Value: alertName, IsEqual: true}}
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
	if p.FilterBuffer() != "" {
		parts = append(parts, "filter "+p.FilterBuffer())
	}
	if p.stateFilter != "" {
		parts = append(parts, "state "+p.stateFilter)
	}
	return strings.Join(parts, ", ")
}

func alertNoun(n int) string {
	if n == 1 {
		return wordAlert
	}
	return wordAlerts
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
	sess := p.session
	var confirmed []string
	if pending.confirmed {
		confirmed = []string{pending.tenant}
	}
	return app.PushPage(func() app.Page {
		return silenceform.New(silenceform.Options{
			Clients:    clients,
			Tenant:     tenant,
			Styles:     styles,
			Now:        now,
			Creator:    creator,
			Matchers:   matchers,
			ScopeNote:  scopeNote,
			SubmitCtx:  submitCtx,
			Guardrails: sess.Guardrails(),
			Action:     guardrail.ActionSilenceCreate,
			Confirmed:  confirmed,
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
// between the confirm modal / bulk-form push and its result.
// Empty between rounds. tenants is a stable alphabetical list of
// distinct tenant names for the confirm question and the form banner.
type pendingBulkSilence struct {
	targets []bulkSilenceTarget
	tenants []string
}

// openBulkSilence resolves the marked groups into bulkSilenceTargets
// (one `alertname=X` silence per marked group, paired with its tenant)
// and either pushes the bulk form directly or opens a confirm modal
// first. One mark skips the modal, unless a guardrail rule asks for a
// confirmation. Marks that no longer correspond to any in-scope
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
	// tenants is what the run resolved to, not what is marked: a marked
	// tenant whose client vanished still counts for the cap, but has no
	// name worth asking the user to type.
	d := p.session.Guardrails().Decide(p.request(func() []string { return tenants }))
	question := fmt.Sprintf("silence %d %s? (tenant %s)",
		len(targets), alertNoun(len(targets)), formatTenantBreakdownAlerts(targets))
	return listpage.OpenBulkForm(len(targets), d, question, p.pushBulkSilenceForm)
}

// silenceRequest asks about the run an `s` press would really fire,
// so the duplicate tenants are the per-tenant count the cap compares
// against.
func (p *Page) silenceRequest() guardrail.Request {
	return p.request(p.markedTargets)
}

// guarded answers the [guarded] suffix. It counts each marked tenant
// once: a binding outlives any one run, so a cap the current marks
// happen to breach must not strike `s` off the hint strip.
func (p *Page) guarded() bool {
	return p.session.Guardrails().Refuses(p.request(p.markedTenants))
}

// request turns the press into its targets; marked resolves the bulk
// fan-out, the one case the callers count differently.
func (p *Page) request(marked func() []string) guardrail.Request {
	switch {
	case len(p.marks) > 0:
		return bulkop.SilenceRequest(true, marked()...)
	case p.Index() < len(p.groups):
		return bulkop.SilenceRequest(false, p.groups[p.Index()].tenant)
	}
	return bulkop.SilenceRequest(false)
}

// markedTargets names the tenant of every marked group, once per group
// and in the page's own row order. It keeps a marked tenant with no
// writeable client, which resolveBulkSilenceTargets drops: refusing a
// press that would have flashed "no writeable backend" costs nothing,
// and aligning the two walks would let a capped or denied tenant
// through whenever its client is missing at that moment.
func (p *Page) markedTargets() []string {
	var out []string
	for _, g := range p.groups {
		if _, marked := p.marks[markKey(g)]; marked {
			out = append(out, g.tenant)
		}
	}
	return out
}

// markedTenants serves the caller that asks per backend rather than
// per row.
func (p *Page) markedTenants() []string {
	var out []string
	for _, g := range p.groups {
		if _, marked := p.marks[markKey(g)]; marked && !slices.Contains(out, g.tenant) {
			out = append(out, g.tenant)
		}
	}
	return out
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
	opts := p.bulkFormOptions(pending)
	return app.PushPage(func() app.Page { return silenceform.New(opts) })
}

// bulkFormOptions omits Guardrails and Action on purpose, which is the
// contract silenceform.Options.Bulk records: a bulk submit returns
// before the form's own gate, so a verb handed over here would name a
// check nothing runs. This page cleared the run already. Whatever the
// page adds to the form belongs in here, where the omission is pinned.
func (p *Page) bulkFormOptions(pending pendingBulkSilence) silenceform.Options {
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	return silenceform.Options{
		Clients:    p.clients,
		Styles:     p.styles,
		Now:        p.now,
		Creator:    creator,
		Bulk:       true,
		BulkBanner: bulkSilenceBanner(pending.targets, pending.tenants),
		SubmitCtx:  p.submitCtx,
	}
}

// bulkSilenceBanner formats the form's banner. Single tenant reads
// "applies to N alerts (tenant prod) — one alertname silence each";
// multi-tenant names the tenant count. Each target is one
// `alertname=X` silence-all, so the wording stresses the per-alertname
// fanout (distinct from the L2 silence-one full-label fanout).
func bulkSilenceBanner(targets []bulkSilenceTarget, tenants []string) string {
	n := len(targets)
	if len(tenants) == 1 {
		return fmt.Sprintf("applies to %d %s (tenant %s) — one alertname silence each", n, alertNoun(n), tenants[0])
	}
	return fmt.Sprintf("applies to %d alerts across %d tenants — one alertname silence each", n, len(tenants))
}

// handleConfirmResult routes a ConfirmResultMsg to whichever round is
// pending — the single-cursor silence-all (count>1) or the marked
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
	p.pendingSilenceAll.confirmed = true
	return p.pushSilenceAllForm()
}

// handleBulkSilenceConfirm consumes a ConfirmResultMsg from the
// pre-form bulk confirm modal. Yes pushes the bulk form; No /
// Cancelled drops the pending state silently. An incoming message
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
	ctx, cancel := bulkop.BeginRound(p.bulkCtx, p.cancelBulk)
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
	dispatch := bulkop.Dispatch(ctx, ops, writer, p.session.BulkConcurrency())
	return bulkop.RunRound(cancel, dispatch)
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
	return bulkop.SilenceResultFlash(total, successes, total-successes, "alerts")
}

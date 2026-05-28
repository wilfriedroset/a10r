// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// bulkSilenceTarget is one marked instance's silence-one work: its
// fingerprint (the bulkop key, also the mark key) plus the full-label
// matcher set the fanout stamps the form metadata onto.
type bulkSilenceTarget struct {
	Fingerprint string
	Matchers    []backend.Matcher
}

// pendingBulkSilence captures the resolved targets between the
// confirm modal (N≥2) / bulk-form push and its result. Empty between
// rounds.
type pendingBulkSilence struct {
	targets []bulkSilenceTarget
}

// silenceOneWarnThreshold is the marked-count at or above which the
// confirm question gains an extra warning line steering the operator
// toward silence-all. N full-label silences are a very different
// artefact from one alertname=X silence-all for nearly the same
// intent — see CONTEXT.md "Silence-one".
const silenceOneWarnThreshold = 10

// openBulkSilence resolves the marked instances into targets and
// either pushes the bulk form directly (N=1) or opens a confirm modal
// first (N≥2). Marks that no longer match any current instance are
// dropped silently.
func (p *Page) openBulkSilence() tea.Cmd {
	if len(p.clients) == 0 {
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
	}
	if _, ok := p.clients[p.tenant]; !ok {
		return footer.ShowFlash(footer.FlashWarn, hintNoWriteableBackend)
	}
	targets := p.resolveBulkSilenceTargets()
	if len(targets) == 0 {
		return footer.ShowFlash(footer.FlashInfo, "no marked instances remain")
	}
	p.pendingBulkSilence = pendingBulkSilence{targets: targets}
	if len(targets) == 1 {
		return p.pushBulkSilenceForm()
	}
	return app.OpenModal(func() modal.Modal {
		return modal.NewConfirm(bulkSilenceQuestion(len(targets), p.tenant), modal.ConfirmDefaultYes)
	})
}

// bulkSilenceQuestion formats the confirm prompt. At or above the
// warn threshold it appends a second line nudging the operator to
// Esc and use silence-all instead of fanning out N full-label
// silences.
func bulkSilenceQuestion(n int, tenant string) string {
	q := fmt.Sprintf("silence %d instances? (tenant %s)", n, tenant)
	if n >= silenceOneWarnThreshold {
		q += fmt.Sprintf("\n%d individual silences will be created — Esc and use silence-all to silence the whole alert instead.", n)
	}
	return q
}

// resolveBulkSilenceTargets walks p.instances (not p.view) so a
// marked instance hidden by an active filter still ends up in the
// queue. Targets are sorted by fingerprint so the fanout order and
// confirm wording are stable across runs / tests.
func (p *Page) resolveBulkSilenceTargets() []bulkSilenceTarget {
	targets := make([]bulkSilenceTarget, 0, len(p.marks))
	for _, a := range p.instances {
		if _, marked := p.marks[a.Fingerprint]; !marked {
			continue
		}
		targets = append(targets, bulkSilenceTarget{
			Fingerprint: a.Fingerprint,
			Matchers:    silenceform.MatchersFromLabels(a.Labels),
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Fingerprint < targets[j].Fingerprint
	})
	return targets
}

// pushBulkSilenceForm pushes the silence form in bulk mode with a
// banner spelling out the per-target fanout shape. Single tenant, so
// the banner always names it.
func (p *Page) pushBulkSilenceForm() tea.Cmd {
	pending := p.pendingBulkSilence
	if len(pending.targets) == 0 {
		return footer.ShowFlash(footer.FlashInfo, "no marked instances remain")
	}
	creator := p.creator
	if creator == "" {
		creator = "a10r"
	}
	styles := p.styles
	now := p.now
	clients := p.clients
	submitCtx := p.submitCtx
	banner := bulkSilenceBanner(len(pending.targets), p.tenant)
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

// bulkSilenceBanner formats the form's banner — "applies to N
// instances (tenant prod) — each silenced with its own labels".
func bulkSilenceBanner(n int, tenant string) string {
	word := "instances"
	if n == 1 {
		word = "instance"
	}
	return fmt.Sprintf("applies to %d %s (tenant %s) — each silenced with its own labels", n, word, tenant)
}

// handleBulkSilenceConfirm consumes the pre-form confirm (N≥2). Yes
// pushes the bulk form; No / Cancelled drops the pending state.
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

// handleBulkSilenceSubmit stamps the form metadata onto every pending
// target and dispatches one CreateSilence per marked instance.
func (p *Page) handleBulkSilenceSubmit(m silenceform.BulkSubmittedMsg) tea.Cmd {
	pending := p.pendingBulkSilence
	p.pendingBulkSilence = pendingBulkSilence{}
	if len(pending.targets) == 0 {
		return nil
	}
	if p.cancelBulk != nil {
		p.cancelBulk()
	}
	parent := p.bulkCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancelBulk = cancel
	clients := p.clients
	tenant := p.tenant
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
		ops = append(ops, bulkop.Op[string]{Key: t.Fingerprint, Tenant: tenant})
	}
	writer := func(ctx context.Context, t string, op bulkop.Op[string]) (string, error) {
		c, ok := clients[t]
		if !ok {
			return "", bulkop.ErrNoWriteableBackend
		}
		spec := specBase
		spec.Matchers = matchersByFP[op.Key]
		return c.CreateSilence(ctx, spec)
	}
	dispatch := bulkop.Dispatch(ctx, ops, writer, p.bulkConcurrency)
	return func() tea.Msg {
		defer cancel()
		return dispatch()
	}
}

// handleBulkSilenceDone applies a completed fanout: successes drop
// their marks, failures keep theirs so the next `s` retries only the
// unfinished work.
func (p *Page) handleBulkSilenceDone(m bulkop.DoneMsg[string]) tea.Cmd {
	total := len(m.Results)
	successes := 0
	for _, r := range m.Results {
		if r.Err == nil {
			delete(p.marks, r.Op.Key)
			slog.Default().Info("silence write succeeded",
				slog.String("op", "created"),
				slog.String("alert_fingerprint", r.Op.Key),
				slog.String("surface", "groupdetail-bulk-silence"))
			successes++
			continue
		}
		if p.logger != nil {
			p.logger.Error("bulk silence: instance silence failed",
				slog.String("backend", r.Op.Tenant),
				slog.String("tenant", r.Op.Tenant),
				slog.String("alert_fingerprint", r.Op.Key),
				slog.String("err", r.Err.Error()),
			)
		}
	}
	return flashBulkSilenceResult(total, successes, total-successes)
}

// flashBulkSilenceResult formats the success / partial / failure
// flash for a completed round.
func flashBulkSilenceResult(total, success, failed int) tea.Cmd {
	if total == 1 {
		if success == 1 {
			return footer.ShowFlash(footer.FlashSuccess, "silence created")
		}
		return footer.ShowFlash(footer.FlashError, "silence failed")
	}
	if failed == 0 {
		return footer.ShowFlash(footer.FlashSuccess, fmt.Sprintf("silenced %d instances", success))
	}
	if success == 0 {
		return footer.ShowFlash(footer.FlashError, fmt.Sprintf("silence failed for %d instances", failed))
	}
	return footer.ShowFlash(footer.FlashWarn, fmt.Sprintf("silenced %d of %d — %d failed", success, total, failed))
}

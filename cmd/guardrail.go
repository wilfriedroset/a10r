// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/output"
)

// guardrailPrefix tags every refusal sentence so a lines-mode reader
// and a log grep both see which feature refused the write.
const guardrailPrefix = "guardrail: "

// guardrailRefusals asks the write policy about one command's resolved
// target set. The targets go in whole, duplicates included, because one
// entry per target is the count max_bulk compares against.
//
// Headless has no modal, so a tenant still owed a typed confirmation is
// a refusal too. The two lists are regrouped into the order the targets
// first name each tenant, so stderr reads in the same order as the plan
// the user asked for rather than grouping every deny ahead of every
// confirmation.
func guardrailRefusals(rules guardrail.Set, action guardrail.Action, targets []writeTarget, confirmed []string) []guardrail.Refusal {
	tenants := make([]string, len(targets))
	for i, t := range targets {
		tenants[i] = t.tenant
	}
	d := rules.Decide(guardrail.Request{Action: action, Tenants: tenants, Confirmed: confirmed})

	byTenant := make(map[string]guardrail.Refusal, len(d.Refusals)+len(d.Typed))
	for _, tenant := range d.Typed {
		byTenant[tenant] = guardrail.Refusal{
			Tenant:  tenant,
			Note:    "needs --confirm-tenant " + tenant,
			Message: fmt.Sprintf("%s requires --confirm-tenant %s", tenant, tenant),
		}
	}
	for _, r := range d.Refusals {
		byTenant[r.Tenant] = r
	}
	var out []guardrail.Refusal
	for _, tenant := range targetTenants(targets) {
		r, ok := byTenant[tenant]
		if !ok {
			continue
		}
		// The gate leaves the sentence unprefixed for the TUI, which
		// flashes it inside chrome that already names the feature.
		r.Message = guardrailPrefix + r.Message
		out = append(out, r)
	}
	return out
}

// ensureGuardrailsAllow is the fail-closed gate the real write path
// runs after ensureWritableTargets and before the first mutation. It
// names every refused tenant at once so a narrowed retry needs one
// round trip, not one per tenant.
func ensureGuardrailsAllow(rules guardrail.Set, action guardrail.Action, targets []writeTarget, confirmed []string) error {
	refusals := guardrailRefusals(rules, action, targets, confirmed)
	if len(refusals) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(refusals)+1)
	for _, r := range refusals {
		msgs = append(msgs, r.Message)
	}
	msgs = append(msgs, "no silence was written")
	return NewExitError(ExitGuardrailRefused, errors.New(strings.Join(msgs, "; ")))
}

// gateWrite is the one gate every write verb passes before runWrites:
// a dry run renders the plan and stops, otherwise the read-only and
// guardrail checks refuse the whole command before the first mutation.
// proceed is true only when the caller should go on and write.
func gateWrite(
	out, errOut io.Writer,
	cfg *config.Config,
	format output.Format,
	action guardrail.Action,
	targets []writeTarget,
	globalReadOnly, dryRun bool,
	confirmTenants []string,
) (proceed bool, err error) {
	if dryRun {
		return false, runDryRun(out, errOut, cfg, format, action, targets, globalReadOnly, confirmTenants)
	}
	if err := ensureWritableTargets(globalReadOnly, cfg, targetTenants(targets)); err != nil {
		return false, err
	}
	if err := ensureGuardrailsAllow(cfg.Guardrails, action, targets, confirmTenants); err != nil {
		return false, err
	}
	return true, nil
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wilfriedroset/a10r/internal/guardrail"
)

// guardrailBlock is one tenant the write policy refuses. note is the
// short form the dry-run plan brackets; message is the sentence stderr
// carries on a real run. Both exist because the two surfaces have
// different room and different readers (ADR 0046, ADR 0049).
type guardrailBlock struct {
	tenant  string
	note    string
	message string
}

// guardrailBlocks evaluates the policy over one command's resolved
// target set and returns an entry per refused tenant, in the order the
// targets first name each tenant.
//
// action is a guardrail.ActionSilence* constant, never a hand-written
// string: a name no rule can match would fail open. Evaluation is per
// tenant because a cap counts the targets landing in one backend, not
// the size of the whole run. confirmed carries the --confirm-tenant
// values, which satisfy a type-tenant-name rule for the named tenant
// only.
//
// Read-only is not consulted here. On the real-run path
// ensureWritableTargets has already refused, and the dry-run path
// skips read-only tenants before calling, so a10r never names a
// guardrail where read-only was the real answer.
func guardrailBlocks(rules guardrail.Set, action string, targets []writeTarget, confirmed []string) []guardrailBlock {
	if len(rules) == 0 {
		return nil
	}
	counts := make(map[string]int, len(targets))
	for _, t := range targets {
		counts[t.tenant]++
	}

	var blocks []guardrailBlock
	for _, tenant := range targetTenants(targets) {
		b := guardrailBlockFor(rules.Evaluate(tenant, action), action, tenant, counts[tenant], confirmed)
		if b != nil {
			blocks = append(blocks, *b)
		}
	}
	return blocks
}

// guardrailPrefix tags every refusal sentence so a lines-mode reader
// and a log grep both see which feature refused the write.
const guardrailPrefix = "guardrail: "

// guardrailBlockFor turns one tenant's verdict into a refusal, or nil
// when the write may proceed. The order is the strength order: a deny
// leaves nothing to say about a cap or a confirmation.
func guardrailBlockFor(v guardrail.Verdict, name, tenant string, count int, confirmed []string) *guardrailBlock {
	switch {
	case v.Denied:
		return &guardrailBlock{tenant: tenant, note: "denied", message: guardrailPrefix + v.DenyMessage(name, tenant)}

	case v.ExceedsBulk(count):
		return &guardrailBlock{
			tenant: tenant,
			note:   fmt.Sprintf("max_bulk %d exceeded", v.MaxBulk),
			message: fmt.Sprintf("%s%s on %s: %d targets exceed max_bulk %d",
				guardrailPrefix, name, tenant, count, v.MaxBulk),
		}

	// A plain confirmation has no headless form: typing the id on the
	// command line already is the deliberate act the modal asks for.
	// Only the typed level adds something a flag can carry.
	case v.Confirmation == guardrail.ConfirmationTypeTenantName && !slices.Contains(confirmed, tenant):
		return &guardrailBlock{
			tenant:  tenant,
			note:    "needs --confirm-tenant " + tenant,
			message: fmt.Sprintf("%s%s requires --confirm-tenant %s", guardrailPrefix, tenant, tenant),
		}
	}
	return nil
}

// ensureGuardrailsAllow is the fail-closed gate the real write path
// runs after ensureWritableTargets and before the first mutation. It
// names every refused tenant at once so a narrowed retry needs one
// round trip, not one per tenant.
func ensureGuardrailsAllow(rules guardrail.Set, action string, targets []writeTarget, confirmed []string) error {
	blocks := guardrailBlocks(rules, action, targets, confirmed)
	if len(blocks) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(blocks)+1)
	for _, b := range blocks {
		msgs = append(msgs, b.message)
	}
	msgs = append(msgs, "no silence was written")
	return NewExitError(ExitGuardrailRefused, errors.New(strings.Join(msgs, "; ")))
}

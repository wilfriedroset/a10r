// SPDX-License-Identifier: Apache-2.0

package guardrail

import (
	"fmt"
	"slices"
)

// Request is one write the policy is asked about.
type Request struct {
	Action Action
	// Tenants carries one entry per resolved target, so duplicates are
	// the per-tenant count max_bulk compares against. It is built
	// before any read-only or client-availability filtering, because a
	// target the run cannot serve still occupies a slot the operator
	// asked for.
	Tenants []string
	// Confirmed are the tenants the user already confirmed, which
	// satisfies a type-tenant-name rule for those tenants only.
	Confirmed []string
	// Lead is the verb the reader recognizes, which differs per
	// surface: the TUI says "bulk expire" because that is the key the
	// user pressed, the headless path says "silence.expire" because
	// that is the rule name to edit. Empty means use Action.
	Lead string
}

// Refusal is one tenant the policy refuses. Note is the short form a
// dry-run plan brackets; Message is the sentence a flash or stderr
// carries. Both exist because the two surfaces have different room and
// different readers (ADR 0046, ADR 0049). The caller adds its own
// prefix.
type Refusal struct {
	Tenant  string
	Note    string
	Message string
}

// Decision is the whole answer for one Request.
type Decision struct {
	// Refusals holds only what no confirmation can clear, so a surface
	// that stops on Refused is fail-closed before it ever prompts.
	Refusals []Refusal
	// Typed lists the tenants still owed a typed prompt, in
	// first-appearance order.
	Typed []string
	// Confirm is the strongest level any tenant in the request demands,
	// not the level still owed: Request.Confirmed clears Typed, never
	// this.
	Confirm Confirmation
}

// Refuses serves the callers that need only the verdict, not the sentence.
func (s Set) Refuses(req Request) bool { return s.Decide(req).Refused() }

// Decide answers one Request: it walks the distinct tenants in
// first-appearance order and folds each verdict in strength order, so
// a deny leaves nothing to say about a cap and a cap leaves nothing to
// say about a confirmation.
func (s Set) Decide(req Request) Decision {
	if len(s) == 0 {
		return Decision{}
	}
	counts := make(map[string]int, len(req.Tenants))
	for _, t := range req.Tenants {
		counts[t]++
	}
	lead := req.Lead
	if lead == "" {
		lead = string(req.Action)
	}

	var d Decision
	seen := make(map[string]bool, len(counts))
	for _, tenant := range req.Tenants {
		if seen[tenant] {
			continue
		}
		seen[tenant] = true

		v := s.evaluate(tenant, req.Action)
		d.Confirm = d.Confirm.Stronger(v.Confirmation)
		typed := v.Confirmation.rank() >= ConfirmationTypeTenantName.rank() && !slices.Contains(req.Confirmed, tenant)
		if typed {
			d.Typed = append(d.Typed, tenant)
		}
		if r, ok := refusal(v, lead, tenant, counts[tenant]); ok {
			d.Refusals = append(d.Refusals, r)
		}
	}
	return d
}

// refusal turns one tenant's verdict into a refusal no confirmation
// can clear, or reports false when only a prompt stands in the way.
func refusal(v Verdict, lead, tenant string, count int) (Refusal, bool) {
	switch {
	case v.Denied:
		return Refusal{Tenant: tenant, Note: "denied", Message: v.denyMessage(lead, tenant)}, true

	case v.exceedsBulk(count):
		return Refusal{
			Tenant:  tenant,
			Note:    fmt.Sprintf("max_bulk %d exceeded", v.MaxBulk),
			Message: v.bulkMessage(lead, tenant, count),
		}, true
	}
	return Refusal{}, false
}

func (d Decision) Refused() bool { return len(d.Refusals) > 0 }

// Flash is the sentence for the first refused tenant, because one
// refusal already stops the whole press. Empty when nothing refuses.
func (d Decision) Flash() string {
	if len(d.Refusals) == 0 {
		return ""
	}
	return d.Refusals[0].Message
}

// Messages names every refused tenant at once, so a narrowed retry
// needs one round trip rather than one per tenant.
func (d Decision) Messages() []string {
	out := make([]string, len(d.Refusals))
	for i, r := range d.Refusals {
		out[i] = r.Message
	}
	return out
}

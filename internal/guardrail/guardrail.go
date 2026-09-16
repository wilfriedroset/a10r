// SPDX-License-Identifier: Apache-2.0

// Package guardrail evaluates the per-tenant write policy declared
// under `guardrails:` in a10r.yaml. It is the single evaluator the
// TUI and the headless CLI both import, so a rule cannot mean one
// thing on a key press and another on a command line.
//
// The package is pure: it answers "what does policy say about this
// tenant and this verb" and never performs, blocks, or counts a
// write itself. Callers own the enforcement — hiding a binding,
// flashing a warning, refusing a command — and own the target count
// that Verdict.ExceedsBulk compares against.
//
// Evaluation is per tenant. A run that spans several backends asks
// once per backend and enforces each answer separately, because a
// cap counts the targets landing in one tenant rather than the size
// of the whole run.
package guardrail

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// The write verbs a rule can name. Bulk is not a separate action: a
// rule matches a bulk run through the same name as its single form,
// and only MaxBulk reads the target count.
const (
	ActionSilenceCreate   = "silence.create"
	ActionSilenceUpdate   = "silence.update"
	ActionSilenceExpire   = "silence.expire"
	ActionSilenceRecreate = "silence.recreate"
)

// knownActions is the closed set a rule's `actions` globs must hit at
// least one of. Order is the one the validation error prints, which
// matches the lifecycle a reader expects rather than the alphabet.
var knownActions = []string{
	ActionSilenceCreate,
	ActionSilenceUpdate,
	ActionSilenceExpire,
	ActionSilenceRecreate,
}

// Confirmation is how hard a10r makes the user work before a write
// lands. The empty value means "this rule has no opinion"; a rule may
// only raise a verb's built-in default, never lower it, so callers
// take the stronger of the two.
type Confirmation string

const (
	// ConfirmationPlain is the existing yes/no modal.
	ConfirmationPlain Confirmation = "plain"
	// ConfirmationTypeTenantName requires the user to type the backend
	// name exactly before the write proceeds.
	ConfirmationTypeTenantName Confirmation = "type-tenant-name"
)

// rank orders confirmation levels weakest to strongest so combination
// is a max. The unset level ranks below plain: a verb that confirms
// nothing today keeps confirming nothing when no rule speaks up.
func (c Confirmation) rank() int {
	switch c {
	case ConfirmationTypeTenantName:
		return 2
	case ConfirmationPlain:
		return 1
	default:
		return 0
	}
}

// Stronger returns the harder of the two levels, so a caller can fold
// a rule's demand onto a verb's built-in default without repeating the
// ordering.
func (c Confirmation) Stronger(other Confirmation) Confirmation {
	if other.rank() > c.rank() {
		return other
	}
	return c
}

// Rule is one entry of the `guardrails:` list. Tenants and Actions are
// glob lists; an empty or absent list matches everything, so a rule
// that names neither applies to every write on every tenant.
//
// Validate rejects a rule that restricts nothing, because such a rule
// is always a typo rather than a deliberate no-op.
type Rule struct {
	Tenants      []string     `yaml:"tenants,omitempty"`
	Actions      []string     `yaml:"actions,omitempty"`
	Deny         bool         `yaml:"deny,omitempty"`
	Confirmation Confirmation `yaml:"confirmation,omitempty"`
	// MaxBulk zero means absent, the same idiom as
	// config.Defaults.BulkConcurrency. A user who wants no bulk run at
	// all writes deny rather than max_bulk: 0.
	MaxBulk int `yaml:"max_bulk,omitempty"`
	// Reason is surfaced to the user on a deny, and nowhere else.
	Reason string `yaml:"reason,omitempty"`
}

// Verdict is what every matching rule says, folded together. The zero
// Verdict means policy is silent: not denied, no cap, no confirmation
// demand.
type Verdict struct {
	Denied bool
	// Reason comes from the first denying rule that carries one, so a
	// reasonless deny never masks an explained one. It is empty when no
	// denying rule gave a reason, and callers must render the refusal
	// without it rather than inventing one.
	Reason string
	// MaxBulk is the smallest cap any matching rule declared. Zero
	// means uncapped.
	MaxBulk int
	// Confirmation is the floor a matching rule demands. Fold it onto
	// the verb's own default with Confirmation.Stronger.
	Confirmation Confirmation
}

// ExceedsBulk reports whether count breaches the cap. An uncapped
// verdict never breaches, however large the run.
func (v Verdict) ExceedsBulk(count int) bool {
	return v.MaxBulk > 0 && count > v.MaxBulk
}

// DenyMessage is the one sentence a surface prints when policy refuses
// a verb. It lives here so the TUI flash and the headless stderr line
// cannot word the same refusal differently. Empty when nothing denies.
func (v Verdict) DenyMessage(action, tenant string) string {
	if !v.Denied {
		return ""
	}
	msg := action + " denied on " + tenant
	if v.Reason != "" {
		msg += ": " + v.Reason
	}
	return msg
}

// Set is the configured rule list. Rule order carries no meaning for
// the outcome; it decides only which of several reasons a multi-deny
// verdict quotes, and the order Validate and UnmatchedTenants report
// in.
//
// Evaluate assumes Validate already passed. On an unvalidated glob it
// fails open — a pattern path.Match cannot compile matches nothing,
// so a deny rule would quietly stop denying. The config loader is the
// one place that gate lives.
type Set []Rule

// Evaluate folds every rule matching tenant and action into one
// verdict: any deny wins, the smallest cap wins, the strongest
// confirmation wins.
func (s Set) Evaluate(tenant, action string) Verdict {
	var v Verdict
	for _, r := range s {
		if !match(r.Tenants, tenant) || !match(r.Actions, action) {
			continue
		}
		if r.Deny {
			v.Denied = true
			if v.Reason == "" {
				v.Reason = r.Reason
			}
		}
		if r.MaxBulk > 0 && (v.MaxBulk == 0 || r.MaxBulk < v.MaxBulk) {
			v.MaxBulk = r.MaxBulk
		}
		v.Confirmation = v.Confirmation.Stronger(r.Confirmation)
	}
	return v
}

// Validate checks every rule and returns the first problem, naming the
// rule by its index in config order. It is fail-closed by design: a
// guardrail the loader cannot understand must stop startup rather than
// silently permit the write it was written to restrict.
func (s Set) Validate() error {
	for i, r := range s {
		if err := r.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (r Rule) validate(i int) error {
	if err := validateGlobs(fmt.Sprintf("guardrails[%d].tenants", i), r.Tenants); err != nil {
		return err
	}
	if err := validateGlobs(fmt.Sprintf("guardrails[%d].actions", i), r.Actions); err != nil {
		return err
	}
	for j, a := range r.Actions {
		if !slices.ContainsFunc(knownActions, func(known string) bool { return matchOne(a, known) }) {
			return fmt.Errorf("guardrails[%d].actions[%d]: unknown action %q (known: %s)", i, j, a, strings.Join(knownActions, ", "))
		}
	}
	switch r.Confirmation {
	case "", ConfirmationPlain, ConfirmationTypeTenantName:
	default:
		return fmt.Errorf("guardrails[%d].confirmation: unknown level %q (known: %s, %s)", i, r.Confirmation, ConfirmationPlain, ConfirmationTypeTenantName)
	}
	if r.MaxBulk < 0 {
		return fmt.Errorf("guardrails[%d].max_bulk: must be >= 1 (got %d); omit the field to leave bulk uncapped", i, r.MaxBulk)
	}
	if !r.Deny && r.Confirmation == "" && r.MaxBulk == 0 {
		return fmt.Errorf("guardrails[%d]: set at least one of deny, confirmation, max_bulk", i)
	}
	return nil
}

// validateGlobs enforces the "star and literals only" syntax. The
// three rejected characters are the rest of path.Match's grammar:
// keeping them out means a pattern can never fail to compile, so
// match never has an error to swallow.
//
// One path.Match trait survives: `*` stops at a `/`, so a backend
// literally named `eu/prod` needs a glob that spells the separator.
// Backend names are identifiers in practice, which is why this is a
// note rather than a hand-written matcher.
func validateGlobs(field string, globs []string) error {
	for i, g := range globs {
		if strings.TrimSpace(g) == "" {
			return fmt.Errorf("%s[%d]: must not be empty", field, i)
		}
		if strings.ContainsAny(g, `?[\`) {
			return fmt.Errorf(`%s[%d]: only "*" wildcards are supported`, field, i)
		}
	}
	return nil
}

// UnmatchedTenants returns the tenant globs that match none of the
// given backend names, deduplicated and in first-appearance order.
// This is a warning, not an error: a config.d fragment shared across
// machines may legitimately name a tenant the local laptop lacks.
func (s Set) UnmatchedTenants(backends []string) []string {
	var out []string
	for _, r := range s {
		for _, g := range r.Tenants {
			if slices.Contains(out, g) {
				continue
			}
			if !slices.ContainsFunc(backends, func(name string) bool { return matchOne(g, name) }) {
				out = append(out, g)
			}
		}
	}
	return out
}

// match reports whether value hits any glob. An empty list matches
// everything, which is what makes `tenants` and `actions` optional.
func match(globs []string, value string) bool {
	if len(globs) == 0 {
		return true
	}
	return slices.ContainsFunc(globs, func(g string) bool { return matchOne(g, value) })
}

// matchOne ignores path.Match's error because validateGlobs rejects
// every pattern that could produce one.
func matchOne(glob, value string) bool {
	ok, _ := path.Match(glob, value)
	return ok
}

// SPDX-License-Identifier: Apache-2.0

// Package guardrail evaluates the `guardrails:` write policy. The TUI and
// the CLI share it so a rule means the same on a key press and a command line.
// It never blocks a write: callers own the enforcement.
package guardrail

import (
	"fmt"
	"slices"
	"strings"
)

// Action is a write verb a rule can name. Pass an Action* constant: an
// untyped literal still converts, and a name no rule matches fails open.
type Action string

// Bulk is not a separate action: a rule matches a bulk run through the
// same name as its single form, and only MaxBulk reads the target count.
const (
	ActionSilenceCreate   Action = "silence.create"
	ActionSilenceUpdate   Action = "silence.update"
	ActionSilenceExpire   Action = "silence.expire"
	ActionSilenceRecreate Action = "silence.recreate"
)

// knownActions is the closed set a rule's `actions` globs must hit at
// least one of. Order is the one the validation error prints, which
// matches the lifecycle a reader expects rather than the alphabet.
var knownActions = []string{
	string(ActionSilenceCreate),
	string(ActionSilenceUpdate),
	string(ActionSilenceExpire),
	string(ActionSilenceRecreate),
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
	// MaxBulk is a pointer so an absent field, which leaves bulk
	// uncapped, is distinguishable from an explicit max_bulk: 0, which
	// Validate rejects. A user who wants no bulk run at all writes deny.
	MaxBulk *int `yaml:"max_bulk,omitempty"`
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

// exceedsBulk reports whether count breaches the cap. An uncapped
// verdict never breaches, however large the run.
func (v Verdict) exceedsBulk(count int) bool {
	return v.MaxBulk > 0 && count > v.MaxBulk
}

// bulkMessage is the one sentence a surface prints when a target count
// breaches the cap. Empty when the count fits. lead names the verb the
// reader recognizes, which differs per surface: the TUI says "bulk
// expire" because that is the key the user pressed, and the headless
// path says "silence.expire" because that is the rule name to edit.
func (v Verdict) bulkMessage(lead, tenant string, count int) string {
	if !v.exceedsBulk(count) {
		return ""
	}
	return fmt.Sprintf("%s on %s: %d targets exceed max_bulk %d", lead, tenant, count, v.MaxBulk)
}

// denyMessage is the one sentence a surface prints when policy refuses
// a verb. It lives here so the TUI flash and the headless stderr line
// cannot word the same refusal differently. Empty when nothing denies.
func (v Verdict) denyMessage(action, tenant string) string {
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
type Set []Rule

// evaluate folds every rule matching tenant and action into one
// verdict: any deny wins, the smallest cap wins, the strongest
// confirmation wins. It stays unexported so Decide is the only way in
// and a new write verb cannot forget the check.
func (s Set) evaluate(tenant string, action Action) Verdict {
	var v Verdict
	for _, r := range s {
		if !match(r.Tenants, tenant) || !match(r.Actions, string(action)) {
			continue
		}
		if r.Deny {
			v.Denied = true
			if v.Reason == "" {
				v.Reason = r.Reason
			}
		}
		if r.MaxBulk != nil && *r.MaxBulk > 0 && (v.MaxBulk == 0 || *r.MaxBulk < v.MaxBulk) {
			v.MaxBulk = *r.MaxBulk
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
	if r.MaxBulk != nil && *r.MaxBulk < 1 {
		return fmt.Errorf("guardrails[%d].max_bulk: must be >= 1 (got %d); omit the field to leave bulk uncapped", i, *r.MaxBulk)
	}
	if !r.Deny && r.Confirmation == "" && r.MaxBulk == nil {
		return fmt.Errorf("guardrails[%d]: set at least one of deny, confirmation, max_bulk", i)
	}
	return nil
}

// validateGlobs enforces the "star and literals only" syntax. `?` and
// `[` are wildcards elsewhere, and `\` is reserved for Literal.
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

// UnmatchedTenantWarning is the one sentence a surface prints for a
// glob UnmatchedTenants returned. It lives here so the `:config`
// page, `a10r info`, and `a10r validate` cannot word the same warning
// differently. The caller adds its own prefix and indent.
func UnmatchedTenantWarning(glob string) string {
	return fmt.Sprintf("tenant glob %q matches no configured backend", glob)
}

// match reports whether value hits any glob. An empty list matches
// everything, which is what makes `tenants` and `actions` optional.
func match(globs []string, value string) bool {
	if len(globs) == 0 {
		return true
	}
	return slices.ContainsFunc(globs, func(g string) bool { return matchOne(g, value) })
}

// Literal quotes a free-form name into a glob that matches only that
// name. It is the one producer of `\`, which validateGlobs keeps out
// of user globs, so a generated rule cannot be spelled in config.
func Literal(name string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`).Replace(name)
}

// matchOne is hand-written because path.Match stops `*` at a `/`, so
// a deny rule on `eu*` would fail open on a backend named `eu/prod`.
func matchOne(glob, value string) bool {
	parts := splitGlob(glob)
	if len(parts) == 1 {
		return parts[0] == value
	}
	first, last := parts[0], parts[len(parts)-1]
	if len(value) < len(first)+len(last) || !strings.HasPrefix(value, first) || !strings.HasSuffix(value, last) {
		return false
	}
	rest := value[len(first) : len(value)-len(last)]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	return true
}

// splitGlob cuts glob at every unescaped `*` and drops the `\` that
// Literal adds, so each part is plain text to search for.
func splitGlob(glob string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(glob); i++ {
		switch {
		case glob[i] == '\\' && i+1 < len(glob):
			i++
			cur.WriteByte(glob[i])
		case glob[i] == '*':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(glob[i])
		}
	}
	return append(parts, cur.String())
}

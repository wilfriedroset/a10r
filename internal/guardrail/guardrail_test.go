// SPDX-License-Identifier: Apache-2.0

package guardrail

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		value    string
		want     bool
	}{
		{name: "empty list matches anything", patterns: nil, value: "prod-eu", want: true},
		{name: "exact", patterns: []string{"prod-eu"}, value: "prod-eu", want: true},
		{name: "exact miss", patterns: []string{"prod-eu"}, value: "prod-us", want: false},
		{name: "star suffix", patterns: []string{"prod-*"}, value: "prod-eu", want: true},
		{name: "star suffix miss", patterns: []string{"prod-*"}, value: "staging-eu", want: false},
		{name: "star prefix", patterns: []string{"*-eu"}, value: "prod-eu", want: true},
		{name: "bare star", patterns: []string{"*"}, value: "anything", want: true},
		{name: "star matches empty run", patterns: []string{"prod*"}, value: "prod", want: true},
		{name: "second pattern hits", patterns: []string{"dev-*", "prod-*"}, value: "prod-eu", want: true},
		{name: "star crosses slash", patterns: []string{"eu*"}, value: "eu/prod", want: true},
		{name: "bare star crosses slash", patterns: []string{"*"}, value: "eu/prod", want: true},
		{name: "inner star crosses slash", patterns: []string{"eu*prod"}, value: "eu/x/prod", want: true},
		{name: "star keeps suffix anchored", patterns: []string{"*-eu"}, value: "prod-eu/x", want: false},
		{name: "consecutive stars", patterns: []string{"a**b"}, value: "ab", want: true},
		{name: "bare star matches empty value", patterns: []string{"*"}, value: "", want: true},
		{name: "literal prefix misses empty value", patterns: []string{"a*"}, value: "", want: false},
		{name: "leading and inner star", patterns: []string{"*b*"}, value: "abc", want: true},
		{name: "prefix and suffix must not overlap", patterns: []string{"ab*ba"}, value: "aba", want: false},
		{name: "two stars in order", patterns: []string{"a*b*c"}, value: "a/c/b", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, match(tt.patterns, tt.value))
		})
	}
}

func TestLiteral(t *testing.T) {
	t.Parallel()

	names := []string{"prod", "prod*", `prod\x`, "prod?", "prod[1]", `*\*`}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.True(t, matchOne(Literal(name), name))
			require.False(t, matchOne(Literal(name), name+"-other"))
		})
	}
}

func TestSetEvaluate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		set    Set
		tenant string
		action Action
		want   Verdict
	}{
		{
			name:   "no rules leaves the verb untouched",
			set:    nil,
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{},
		},
		{
			name:   "deny on a matching tenant and action",
			set:    Set{{Tenants: []string{"prod-*"}, Actions: []string{"silence.expire"}, Deny: true, Reason: "use the change ticket"}},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Denied: true, Reason: "use the change ticket"},
		},
		{
			name:   "deny skipped on a non-matching action",
			set:    Set{{Tenants: []string{"prod-*"}, Actions: []string{"silence.expire"}, Deny: true}},
			tenant: "prod-eu",
			action: ActionSilenceCreate,
			want:   Verdict{},
		},
		{
			name:   "deny skipped on a non-matching tenant",
			set:    Set{{Tenants: []string{"prod-*"}, Deny: true}},
			tenant: "staging-eu",
			action: ActionSilenceExpire,
			want:   Verdict{},
		},
		{
			name:   "deny on a star glob covers a tenant with a slash",
			set:    Set{{Tenants: []string{"eu*"}, Deny: true}},
			tenant: "eu/prod",
			action: ActionSilenceExpire,
			want:   Verdict{Denied: true},
		},
		{
			name:   "absent tenants and actions match everything",
			set:    Set{{Deny: true}},
			tenant: "anything",
			action: ActionSilenceUpdate,
			want:   Verdict{Denied: true},
		},
		{
			name: "any deny wins and carries its own reason",
			set: Set{
				{Tenants: []string{"prod-*"}, MaxBulk: new(5)},
				{Tenants: []string{"*"}, Deny: true, Reason: "frozen"},
			},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Denied: true, Reason: "frozen", MaxBulk: 5},
		},
		{
			name: "smallest max_bulk wins",
			set: Set{
				{Tenants: []string{"*"}, MaxBulk: new(20)},
				{Tenants: []string{"prod-*"}, MaxBulk: new(5)},
				{Tenants: []string{"prod-eu"}, MaxBulk: new(50)},
			},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{MaxBulk: 5},
		},
		{
			name: "strongest confirmation wins regardless of rule order",
			set: Set{
				{Tenants: []string{"prod-eu"}, Confirmation: ConfirmationTypeTenantName},
				{Tenants: []string{"*"}, Confirmation: ConfirmationPlain},
			},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Confirmation: ConfirmationTypeTenantName},
		},
		{
			name: "a weaker rule never lowers a stronger one",
			set: Set{
				{Tenants: []string{"*"}, Confirmation: ConfirmationPlain},
				{Tenants: []string{"prod-eu"}, Confirmation: ConfirmationTypeTenantName},
			},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Confirmation: ConfirmationTypeTenantName},
		},
		{
			name: "the first reason wins when two rules deny",
			set: Set{
				{Deny: true, Reason: "first"},
				{Deny: true, Reason: "second"},
			},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Denied: true, Reason: "first"},
		},
		{
			name: "a reasonless deny does not mask an explained one",
			set: Set{
				{Deny: true},
				{Deny: true, Reason: "frozen for the release"},
			},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Denied: true, Reason: "frozen for the release"},
		},
		{
			name:   "an absent max_bulk leaves the verdict uncapped",
			set:    Set{{Tenants: []string{"prod-*"}, Confirmation: ConfirmationPlain}},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{Confirmation: ConfirmationPlain},
		},
		{
			// Validate rejects these, so reaching Evaluate means the
			// loader was bypassed. Folding one in would win the min
			// against every real cap and silently uncap the run.
			name:   "an unvalidated non-positive cap never folds in",
			set:    Set{{Tenants: []string{"prod-*"}, MaxBulk: new(0)}, {Tenants: []string{"prod-*"}, MaxBulk: new(-1)}, {Tenants: []string{"prod-*"}, MaxBulk: new(5)}},
			tenant: "prod-eu",
			action: ActionSilenceExpire,
			want:   Verdict{MaxBulk: 5},
		},
		{
			name:   "an action glob matches every verb under it",
			set:    Set{{Actions: []string{"silence.*"}, MaxBulk: new(3)}},
			tenant: "prod-eu",
			action: ActionSilenceRecreate,
			want:   Verdict{MaxBulk: 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.set.evaluate(tt.tenant, tt.action))
		})
	}
}

func TestConfirmationStronger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		base  Confirmation
		other Confirmation
		want  Confirmation
	}{
		{name: "both unset", want: ""},
		{name: "a verb default survives a silent rule", base: ConfirmationPlain, other: "", want: ConfirmationPlain},
		{name: "a rule raises a silent verb", base: "", other: ConfirmationTypeTenantName, want: ConfirmationTypeTenantName},
		{name: "a rule raises a plain verb", base: ConfirmationPlain, other: ConfirmationTypeTenantName, want: ConfirmationTypeTenantName},
		{name: "a plain rule never lowers a typed verb", base: ConfirmationTypeTenantName, other: ConfirmationPlain, want: ConfirmationTypeTenantName},
		{name: "an unset rule never lowers a typed verb", base: ConfirmationTypeTenantName, other: "", want: ConfirmationTypeTenantName},
		{name: "equal levels are idempotent", base: ConfirmationPlain, other: ConfirmationPlain, want: ConfirmationPlain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.base.Stronger(tt.other))
		})
	}
}

func TestVerdictExceedsBulk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		v     Verdict
		count int
		want  bool
	}{
		{name: "no cap", v: Verdict{}, count: 1000, want: false},
		{name: "under the cap", v: Verdict{MaxBulk: 20}, count: 19, want: false},
		{name: "at the cap", v: Verdict{MaxBulk: 20}, count: 20, want: false},
		{name: "over the cap", v: Verdict{MaxBulk: 20}, count: 21, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.v.exceedsBulk(tt.count))
		})
	}
}

func TestSetValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		set     Set
		wantErr string
	}{
		{
			name: "a fully populated rule is accepted",
			set: Set{{
				Tenants:      []string{"prod-*"},
				Actions:      []string{"silence.expire"},
				Deny:         true,
				Confirmation: ConfirmationTypeTenantName,
				MaxBulk:      new(20),
				Reason:       "change ticket only",
			}},
		},
		{
			name: "max_bulk alone is an effect",
			set:  Set{{MaxBulk: new(1)}},
		},
		{
			name:    "rule with no effect",
			set:     Set{{Tenants: []string{"prod-*"}}},
			wantErr: `guardrails[0]: set at least one of deny, confirmation, max_bulk`,
		},
		{
			name:    "unknown action",
			set:     Set{{Actions: []string{"silence.delete"}, Deny: true}},
			wantErr: `guardrails[0].actions[0]: unknown action "silence.delete" (known: silence.create, silence.update, silence.expire, silence.recreate)`,
		},
		{
			name:    "unknown confirmation",
			set:     Set{{Confirmation: "two-man-rule"}},
			wantErr: `guardrails[0].confirmation: unknown level "two-man-rule" (known: plain, type-tenant-name)`,
		},
		{
			name:    "an explicit max_bulk of zero",
			set:     Set{{MaxBulk: new(0)}},
			wantErr: `guardrails[0].max_bulk: must be >= 1 (got 0); omit the field to leave bulk uncapped`,
		},
		{
			name:    "max_bulk below one",
			set:     Set{{MaxBulk: new(-1)}},
			wantErr: `guardrails[0].max_bulk: must be >= 1 (got -1); omit the field to leave bulk uncapped`,
		},
		{
			name:    "question mark in a tenant glob",
			set:     Set{{Tenants: []string{"prod-e?"}, Deny: true}},
			wantErr: `guardrails[0].tenants[0]: only "*" wildcards are supported`,
		},
		{
			name:    "bracket in a tenant glob",
			set:     Set{{Tenants: []string{"prod-[eu]"}, Deny: true}},
			wantErr: `guardrails[0].tenants[0]: only "*" wildcards are supported`,
		},
		{
			name:    "backslash in a tenant glob",
			set:     Set{{Tenants: []string{`prod\-eu`}, Deny: true}},
			wantErr: `guardrails[0].tenants[0]: only "*" wildcards are supported`,
		},
		{
			name:    "empty tenant glob",
			set:     Set{{Tenants: []string{"  "}, Deny: true}},
			wantErr: `guardrails[0].tenants[0]: must not be empty`,
		},
		{
			name:    "the index names the offending rule",
			set:     Set{{Deny: true}, {Tenants: []string{"prod-*"}}},
			wantErr: `guardrails[1]: set at least one of deny, confirmation, max_bulk`,
		},
		{
			name:    "glob metacharacter in an action pattern",
			set:     Set{{Actions: []string{"silence.?reate"}, Deny: true}},
			wantErr: `guardrails[0].actions[0]: only "*" wildcards are supported`,
		},
		{
			name: "action glob is accepted when it matches a known action",
			set:  Set{{Actions: []string{"silence.*"}, Deny: true}},
		},
		{
			name:    "action glob matching nothing is rejected",
			set:     Set{{Actions: []string{"alert.*"}, Deny: true}},
			wantErr: `guardrails[0].actions[0]: unknown action "alert.*" (known: silence.create, silence.update, silence.expire, silence.recreate)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.set.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestSetUnmatchedTenants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		set      Set
		backends []string
		want     []string
	}{
		{
			name:     "every glob matches a backend",
			set:      Set{{Tenants: []string{"prod-*"}, Deny: true}},
			backends: []string{"prod-eu", "staging"},
			want:     nil,
		},
		{
			name:     "a glob that matches nothing is reported once",
			set:      Set{{Tenants: []string{"prod-*", "lab-*"}, Deny: true}, {Tenants: []string{"lab-*"}, MaxBulk: new(3)}},
			backends: []string{"prod-eu"},
			want:     []string{"lab-*"},
		},
		{
			name:     "the catch-all glob is never unmatched",
			set:      Set{{Tenants: []string{"*"}, Deny: true}},
			backends: []string{"prod-eu"},
			want:     nil,
		},
		{
			name:     "a rule without tenants is never unmatched",
			set:      Set{{Deny: true}},
			backends: []string{"prod-eu"},
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.set.UnmatchedTenants(tt.backends))
		})
	}
}

func TestVerdict_DenyMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict Verdict
		want    string
	}{
		{
			name:    "a reason is appended after a colon",
			verdict: Verdict{Denied: true, Reason: "use the change ticket"},
			want:    "silence.expire denied on prod-eu: use the change ticket",
		},
		{
			name:    "without a reason the sentence ends at the tenant",
			verdict: Verdict{Denied: true},
			want:    "silence.expire denied on prod-eu",
		},
		{
			name:    "a verdict that does not deny has nothing to say",
			verdict: Verdict{MaxBulk: 3},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.verdict.denyMessage(string(ActionSilenceExpire), "prod-eu"))
		})
	}
}

func TestVerdict_BulkMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict Verdict
		lead    string
		count   int
		want    string
	}{
		{
			name:    "the TUI leads with the verb the user pressed",
			verdict: Verdict{MaxBulk: 20},
			lead:    "bulk expire",
			count:   25,
			want:    "bulk expire on prod-eu: 25 targets exceed max_bulk 20",
		},
		{
			name:    "the headless surface leads with the action name",
			verdict: Verdict{MaxBulk: 20},
			lead:    string(ActionSilenceExpire),
			count:   25,
			want:    "silence.expire on prod-eu: 25 targets exceed max_bulk 20",
		},
		{
			name:    "a count inside the cap has nothing to say",
			verdict: Verdict{MaxBulk: 20},
			lead:    "bulk expire",
			count:   20,
			want:    "",
		},
		{
			name:    "an uncapped verdict has nothing to say",
			verdict: Verdict{},
			lead:    "bulk expire",
			count:   99,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.verdict.bulkMessage(tt.lead, "prod-eu", tt.count))
		})
	}
}

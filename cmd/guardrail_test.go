// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/output"
)

func TestGuardrailRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rules     guardrail.Set
		action    guardrail.Action
		targets   []writeTarget
		confirmed []string
		want      []guardrail.Refusal
	}{
		{
			name:    "no rules blocks nothing",
			rules:   nil,
			action:  guardrail.ActionSilenceExpire,
			targets: []writeTarget{{tenant: "prod-eu"}},
		},
		{
			name:    "a deny names the verb, the tenant, and the reason",
			rules:   guardrail.Set{{Tenants: []string{"prod-*"}, Actions: []string{"silence.expire"}, Deny: true, Reason: "use the change ticket"}},
			action:  guardrail.ActionSilenceExpire,
			targets: []writeTarget{{tenant: "prod-eu"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied: use the change ticket",
				Message: "guardrail: silence.expire denied on prod-eu: use the change ticket",
			}},
		},
		{
			name:    "a reasonless deny stops after the tenant",
			rules:   guardrail.Set{{Deny: true}},
			action:  guardrail.ActionSilenceCreate,
			targets: []writeTarget{{tenant: "prod-eu"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied",
				Message: "guardrail: silence.create denied on prod-eu",
			}},
		},
		{
			name:    "an explicit create glob catches the create verb",
			rules:   guardrail.Set{{Actions: []string{"silence.create"}, Deny: true}},
			action:  guardrail.ActionSilenceCreate,
			targets: []writeTarget{{tenant: "prod-eu"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied",
				Message: "guardrail: silence.create denied on prod-eu",
			}},
		},
		{
			name:    "an explicit update glob catches the update verb",
			rules:   guardrail.Set{{Actions: []string{"silence.update"}, Deny: true}},
			action:  guardrail.ActionSilenceUpdate,
			targets: []writeTarget{{tenant: "prod-eu"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied",
				Message: "guardrail: silence.update denied on prod-eu",
			}},
		},
		{
			name:    "an explicit recreate glob catches the recreate verb",
			rules:   guardrail.Set{{Actions: []string{"silence.recreate"}, Deny: true}},
			action:  guardrail.ActionSilenceRecreate,
			targets: []writeTarget{{tenant: "prod-eu"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied",
				Message: "guardrail: silence.recreate denied on prod-eu",
			}},
		},
		{
			name:    "a rule on another verb leaves this one alone",
			rules:   guardrail.Set{{Actions: []string{"silence.expire"}, Deny: true}},
			action:  guardrail.ActionSilenceCreate,
			targets: []writeTarget{{tenant: "prod-eu"}},
		},
		{
			name:   "the cap counts targets per tenant, not per run",
			rules:  guardrail.Set{{MaxBulk: new(2)}},
			action: guardrail.ActionSilenceExpire,
			targets: []writeTarget{
				{tenant: "prod-eu", id: "a"},
				{tenant: "prod-eu", id: "b"},
				{tenant: "prod-eu", id: "c"},
				{tenant: "staging", id: "d"},
				{tenant: "staging", id: "e"},
			},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "max_bulk 2 exceeded",
				Message: "guardrail: silence.expire on prod-eu: 3 targets exceed max_bulk 2",
			}},
		},
		{
			name:    "a typed confirmation demands the flag",
			rules:   guardrail.Set{{Tenants: []string{"prod-*"}, Confirmation: guardrail.ConfirmationTypeTenantName}},
			action:  guardrail.ActionSilenceUpdate,
			targets: []writeTarget{{tenant: "prod-eu"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "needs --confirm-tenant prod-eu",
				Message: "guardrail: silence.update on prod-eu requires --confirm-tenant prod-eu",
			}},
		},
		{
			name:      "the flag satisfies the rule for that tenant only",
			rules:     guardrail.Set{{Confirmation: guardrail.ConfirmationTypeTenantName}},
			action:    guardrail.ActionSilenceUpdate,
			targets:   []writeTarget{{tenant: "prod-eu"}, {tenant: "prod-us"}},
			confirmed: []string{"prod-eu"},
			want: []guardrail.Refusal{{
				Tenant:  "prod-us",
				Note:    "needs --confirm-tenant prod-us",
				Message: "guardrail: silence.update on prod-us requires --confirm-tenant prod-us",
			}},
		},
		{
			name:    "a plain confirmation demands nothing headless",
			rules:   guardrail.Set{{Confirmation: guardrail.ConfirmationPlain}},
			action:  guardrail.ActionSilenceExpire,
			targets: []writeTarget{{tenant: "prod-eu"}},
		},
		{
			name: "deny outranks the cap and the confirmation on one tenant",
			rules: guardrail.Set{
				{Deny: true, Reason: "frozen"},
				{MaxBulk: new(1)},
				{Confirmation: guardrail.ConfirmationTypeTenantName},
			},
			action:  guardrail.ActionSilenceExpire,
			targets: []writeTarget{{tenant: "prod-eu", id: "a"}, {tenant: "prod-eu", id: "b"}},
			want: []guardrail.Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied: frozen",
				Message: "guardrail: silence.expire denied on prod-eu: frozen",
			}},
		},
		{
			name:   "blocked tenants report in first-target order",
			rules:  guardrail.Set{{Deny: true}},
			action: guardrail.ActionSilenceExpire,
			targets: []writeTarget{
				{tenant: "staging", id: "a"},
				{tenant: "prod-eu", id: "b"},
				{tenant: "staging", id: "c"},
			},
			want: []guardrail.Refusal{
				{Tenant: "staging", Note: "denied", Message: "guardrail: silence.expire denied on staging"},
				{Tenant: "prod-eu", Note: "denied", Message: "guardrail: silence.expire denied on prod-eu"},
			},
		},
		{
			name: "a typed refusal keeps its target order against a deny",
			rules: guardrail.Set{
				{Tenants: []string{"staging"}, Confirmation: guardrail.ConfirmationTypeTenantName},
				{Tenants: []string{"prod-eu"}, Deny: true},
			},
			action: guardrail.ActionSilenceExpire,
			targets: []writeTarget{
				{tenant: "staging", id: "a"},
				{tenant: "prod-eu", id: "b"},
			},
			want: []guardrail.Refusal{
				{Tenant: "staging", Note: "needs --confirm-tenant staging", Message: "guardrail: silence.expire on staging requires --confirm-tenant staging"},
				{Tenant: "prod-eu", Note: "denied", Message: "guardrail: silence.expire denied on prod-eu"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, guardrailRefusals(tt.rules, tt.action, tt.targets, tt.confirmed))
		})
	}
}

func TestEnsureGuardrailsAllow(t *testing.T) {
	t.Parallel()

	t.Run("a clean target set returns nil", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ensureGuardrailsAllow(nil, guardrail.ActionSilenceExpire, []writeTarget{{tenant: "prod-eu"}}, nil))
	})

	t.Run("a refusal exits 6 and says nothing was written", func(t *testing.T) {
		t.Parallel()
		rules := guardrail.Set{{Tenants: []string{"prod-*"}, Deny: true, Reason: "frozen"}}
		err := ensureGuardrailsAllow(rules, guardrail.ActionSilenceExpire, []writeTarget{{tenant: "prod-eu"}}, nil)
		require.EqualError(t, err,
			"guardrail: silence.expire denied on prod-eu: frozen; no silence was written")
		require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))
	})

	t.Run("a deny and a typed refusal read in target order", func(t *testing.T) {
		t.Parallel()
		rules := guardrail.Set{
			{Tenants: []string{"staging"}, Confirmation: guardrail.ConfirmationTypeTenantName},
			{Tenants: []string{"prod-eu"}, Deny: true},
		}
		err := ensureGuardrailsAllow(rules, guardrail.ActionSilenceExpire,
			[]writeTarget{{tenant: "staging"}, {tenant: "prod-eu"}}, nil)
		require.EqualError(t, err,
			"guardrail: silence.expire on staging requires --confirm-tenant staging; guardrail: silence.expire denied on prod-eu; no silence was written")
	})

	t.Run("every blocked tenant is named at once", func(t *testing.T) {
		t.Parallel()
		rules := guardrail.Set{{Deny: true}}
		err := ensureGuardrailsAllow(rules, guardrail.ActionSilenceExpire,
			[]writeTarget{{tenant: "prod-eu"}, {tenant: "prod-us"}}, nil)
		require.EqualError(t, err,
			"guardrail: silence.expire denied on prod-eu; guardrail: silence.expire denied on prod-us; no silence was written")
	})
}

// TestRunDryRun_Guardrail pins that the plan line carries the
// refusal in a bracket, and a dry run that would be refused exits with
// the same code the real run would.
func TestRunDryRun_Guardrail(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu"})
	cfg.Guardrails = guardrail.Set{{Tenants: []string{"prod-*"}, Deny: true, Reason: "change ticket only"}}
	targets := []writeTarget{{tenant: "prod-eu", id: "sil-1"}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", guardrail.ActionSilenceExpire, targets, false, nil)
	require.Contains(t, out.String(), "[guardrail: denied: change ticket only]")
	require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))
}

func TestRunDryRun_GuardrailMaxBulkNote(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu"})
	cfg.Guardrails = guardrail.Set{{MaxBulk: new(1)}}
	targets := []writeTarget{{tenant: "prod-eu", id: "a"}, {tenant: "prod-eu", id: "b"}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", guardrail.ActionSilenceExpire, targets, false, nil)
	require.Contains(t, out.String(), "[guardrail: max_bulk 1 exceeded]")
	require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))
}

func TestRunDryRun_GuardrailConfirmTenantNote(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu"})
	cfg.Guardrails = guardrail.Set{{Confirmation: guardrail.ConfirmationTypeTenantName}}
	targets := []writeTarget{{tenant: "prod-eu", id: "sil-1"}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", guardrail.ActionSilenceExpire, targets, false, nil)
	require.Contains(t, out.String(), "[guardrail: needs --confirm-tenant prod-eu]")
	require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))

	out.Reset()
	errOut.Reset()
	err = runDryRun(&out, &errOut, cfg, "", guardrail.ActionSilenceExpire, targets, false, []string{"prod-eu"})
	require.NoError(t, err, "the flag satisfies the rule, so the plan is clean")
	require.NotContains(t, out.String(), "guardrail")
}

// TestRunDryRun_ReadOnlyOutranksTheGuardrail holds the dry run to what
// TestSilenceExpire_ReadOnlyOutranksTheGuardrail holds the real run
// to: ADR 0049 puts read-only first, so neither surface names a user
// rule on a backend that was already refused for another reason.
func TestRunDryRun_ReadOnlyOutranksTheGuardrail(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu", ReadOnly: true})
	cfg.Guardrails = guardrail.Set{{Deny: true, Reason: "frozen"}}
	targets := []writeTarget{{tenant: "prod-eu", id: "sil-1"}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", guardrail.ActionSilenceExpire, targets, false, nil)
	require.Error(t, err)
	require.Equal(t, ExitRuntimeError, exitCodeFor(err), "read-only owns the exit code too")
	require.Contains(t, out.String(), "[read-only: apply would be refused]")
	require.NotContains(t, out.String(), "guardrail")
}

func TestRunDryRun_GuardrailStructuredField(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu"})
	cfg.Guardrails = guardrail.Set{{Deny: true}}
	targets := []writeTarget{{tenant: "prod-eu", id: "sil-1"}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, output.FormatJSON, guardrail.ActionSilenceExpire, targets, false, nil)
	require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))

	var got []plannedWrite
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "denied", got[0].Guardrail)
}

// TestSilenceExpire_GuardrailRefusesBeforeAnyWrite pins that the
// refusal lands after target resolution and before the first mutation,
// and it refuses the whole command.
func TestSilenceExpire_GuardrailRefusesBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod-eu"})
	cfg.Guardrails = guardrail.Set{{Actions: []string{"silence.expire"}, Deny: true, Reason: "frozen"}}
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", false, nil)
	require.EqualError(t, err, "guardrail: silence.expire denied on prod-eu: frozen; no silence was written")
	require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))
	require.Empty(t, client.expired, "nothing is expired when the policy refuses")
}

// TestSilenceCreate_ConfirmTenantSatisfiesTheTypedRule pins the flag:
// it clears a type-tenant-name rule for the tenant it names.
func TestSilenceCreate_ConfirmTenantSatisfiesTheTypedRule(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu"})
	cfg.Guardrails = guardrail.Set{{Confirmation: guardrail.ConfirmationTypeTenantName}}
	opts := silenceCreateOptions{
		Matchers: []string{`a="b"`},
		Ends:     "2h",
		Comment:  "m",
	}

	client := &silenceWriteClient{createID: "new-1"}
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true, opts, "alice", "")
	require.Equal(t, ExitGuardrailRefused, exitCodeFor(err))
	require.Nil(t, client.created)

	opts.ConfirmTenants = []string{"prod-eu"}
	out.Reset()
	errOut.Reset()
	require.NoError(t, silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true, opts, "alice", ""))
	require.NotNil(t, client.created)
}

// TestSilenceExpire_ReadOnlyOutranksTheGuardrail pins that read-only
// outranks a rule on the real write path: a backend that is both
// read-only and denied by a rule reports read-only, because that gate
// runs first.
func TestSilenceExpire_ReadOnlyOutranksTheGuardrail(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod", ReadOnly: true})
	cfg.Guardrails = guardrail.Set{{Deny: true, Reason: "frozen"}}
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", false, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "guardrail")
	require.Empty(t, client.expired)
}

// TestRunDryRun_CapCountsPerTenant pins the one counting rule: max_bulk
// compares against the targets landing in one tenant, so a read-only
// tenant elsewhere in the run neither adds to another tenant's count
// nor gets named by the rule itself (ADR 0049).
func TestRunDryRun_CapCountsPerTenant(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod-eu"}, config.Backend{Name: "prod-ro", ReadOnly: true})
	cfg.Guardrails = guardrail.Set{{MaxBulk: new(2)}}
	targets := []writeTarget{
		{tenant: "prod-eu", id: "a"},
		{tenant: "prod-eu", id: "b"},
		{tenant: "prod-eu", id: "c"},
		{tenant: "prod-ro", id: "d"},
		{tenant: "prod-ro", id: "e"},
		{tenant: "prod-ro", id: "f"},
	}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", guardrail.ActionSilenceExpire, targets, false, nil)
	require.Equal(t, ExitRuntimeError, exitCodeFor(err),
		"the real run refuses this plan for read-only before it reaches the rule")
	require.Contains(t, out.String(), "would expire prod-eu a [guardrail: max_bulk 2 exceeded]")
	require.Contains(t, out.String(), "would expire prod-ro d [read-only: apply would be refused]\n")
}

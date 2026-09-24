// SPDX-License-Identifier: Apache-2.0

package guardrail

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetDecide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  Set
		req  Request
		want Decision
	}{
		{
			name: "no rules allows the write",
			set:  nil,
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu"}},
			want: Decision{},
		},
		{
			name: "a silent policy allows the write",
			set:  Set{{Tenants: []string{"lab-*"}, Deny: true}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu"}},
			want: Decision{},
		},
		{
			name: "deny refuses with its reason",
			set:  Set{{Tenants: []string{"prod-*"}, Deny: true, Reason: "use the change ticket"}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu"}},
			want: Decision{Refusals: []Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied",
				Message: "silence.expire denied on prod-eu: use the change ticket",
			}}},
		},
		{
			name: "a breached cap refuses",
			set:  Set{{Tenants: []string{"prod-*"}, MaxBulk: new(2)}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu", "prod-eu", "prod-eu"}},
			want: Decision{Refusals: []Refusal{{
				Tenant:  "prod-eu",
				Note:    "max_bulk 2 exceeded",
				Message: "silence.expire on prod-eu: 3 targets exceed max_bulk 2",
			}}},
		},
		{
			name: "duplicates are the count, so a fitting run passes",
			set:  Set{{Tenants: []string{"prod-*"}, MaxBulk: new(3)}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu", "prod-eu", "prod-eu"}},
			want: Decision{},
		},
		{
			name: "the cap counts per tenant, not per run",
			set:  Set{{MaxBulk: new(2)}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu", "prod-us", "prod-eu", "prod-us"}},
			want: Decision{},
		},
		{
			name: "a plain confirmation is owed but never refuses",
			set:  Set{{Tenants: []string{"prod-*"}, Confirmation: ConfirmationPlain}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu"}},
			want: Decision{Confirm: ConfirmationPlain},
		},
		{
			name: "an unconfirmed typed level is owed and never refuses",
			set:  Set{{Tenants: []string{"prod-*"}, Confirmation: ConfirmationTypeTenantName}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu"}},
			want: Decision{
				Typed:   []string{"prod-eu"},
				Confirm: ConfirmationTypeTenantName,
			},
		},
		{
			name: "a tenant inside its cap does not hide the one over it",
			set: Set{
				{Tenants: []string{"prod-eu"}, MaxBulk: new(3)},
				{Tenants: []string{"prod-us"}, MaxBulk: new(1)},
			},
			req: Request{
				Action:  ActionSilenceExpire,
				Tenants: []string{"prod-eu", "prod-eu", "prod-eu", "prod-us", "prod-us"},
			},
			want: Decision{Refusals: []Refusal{{
				Tenant:  "prod-us",
				Note:    "max_bulk 1 exceeded",
				Message: "silence.expire on prod-us: 2 targets exceed max_bulk 1",
			}}},
		},
		{
			name: "only the tenants a typed rule covers are owed a prompt, in request order",
			set: Set{
				{Tenants: []string{"prod-*"}, Confirmation: ConfirmationTypeTenantName},
				{Tenants: []string{"staging"}, Confirmation: ConfirmationPlain},
			},
			req: Request{
				Action:  ActionSilenceExpire,
				Tenants: []string{"prod-us", "staging", "prod-eu"},
			},
			want: Decision{
				Typed:   []string{"prod-us", "prod-eu"},
				Confirm: ConfirmationTypeTenantName,
			},
		},
		{
			name: "deny beats a breached cap",
			set: Set{
				{Tenants: []string{"prod-*"}, Deny: true},
				{Tenants: []string{"prod-*"}, MaxBulk: new(1)},
			},
			req: Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu", "prod-eu"}},
			want: Decision{Refusals: []Refusal{{
				Tenant:  "prod-eu",
				Note:    "denied",
				Message: "silence.expire denied on prod-eu",
			}}},
		},
		{
			name: "a breached cap beats an owed confirmation",
			set: Set{
				{Tenants: []string{"prod-*"}, MaxBulk: new(1)},
				{Tenants: []string{"prod-*"}, Confirmation: ConfirmationTypeTenantName},
			},
			req: Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu", "prod-eu"}},
			want: Decision{
				Refusals: []Refusal{{
					Tenant:  "prod-eu",
					Note:    "max_bulk 1 exceeded",
					Message: "silence.expire on prod-eu: 2 targets exceed max_bulk 1",
				}},
				Typed:   []string{"prod-eu"},
				Confirm: ConfirmationTypeTenantName,
			},
		},
		{
			name: "refusals follow the first appearance of each tenant",
			set:  Set{{Deny: true}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-us", "prod-eu", "prod-us", "lab-1"}},
			want: Decision{Refusals: []Refusal{
				{Tenant: "prod-us", Note: "denied", Message: "silence.expire denied on prod-us"},
				{Tenant: "prod-eu", Note: "denied", Message: "silence.expire denied on prod-eu"},
				{Tenant: "lab-1", Note: "denied", Message: "silence.expire denied on lab-1"},
			}},
		},
		{
			name: "a confirmed tenant drops out, the other stays",
			set:  Set{{Confirmation: ConfirmationTypeTenantName}},
			req: Request{
				Action:    ActionSilenceExpire,
				Tenants:   []string{"prod-eu", "prod-us"},
				Confirmed: []string{"prod-eu"},
			},
			want: Decision{
				Typed:   []string{"prod-us"},
				Confirm: ConfirmationTypeTenantName,
			},
		},
		{
			name: "a denied tenant owed a typed prompt lands in both",
			set: Set{
				{Tenants: []string{"prod-*"}, Deny: true, Reason: "frozen"},
				{Tenants: []string{"prod-*"}, Confirmation: ConfirmationTypeTenantName},
			},
			req: Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu"}},
			want: Decision{
				Refusals: []Refusal{{
					Tenant:  "prod-eu",
					Note:    "denied",
					Message: "silence.expire denied on prod-eu: frozen",
				}},
				Typed:   []string{"prod-eu"},
				Confirm: ConfirmationTypeTenantName,
			},
		},
		{
			name: "an unruled tenant beside a ruled one still owes the prompt",
			set:  Set{{Tenants: []string{"staging"}, Confirmation: ConfirmationPlain}},
			req:  Request{Action: ActionSilenceExpire, Tenants: []string{"prod-eu", "staging"}},
			want: Decision{Confirm: ConfirmationPlain},
		},
		{
			name: "the strongest level across the request wins",
			set: Set{
				{Tenants: []string{"lab-*"}, Confirmation: ConfirmationPlain},
				{Tenants: []string{"prod-*"}, Confirmation: ConfirmationTypeTenantName},
			},
			req: Request{
				Action:    ActionSilenceExpire,
				Tenants:   []string{"lab-1", "prod-eu"},
				Confirmed: []string{"prod-eu"},
			},
			want: Decision{Typed: nil, Confirm: ConfirmationTypeTenantName},
		},
		{
			name: "lead renames the verb without changing the outcome",
			set: Set{
				{Tenants: []string{"prod-eu"}, Deny: true, Reason: "frozen"},
				{Tenants: []string{"prod-us"}, MaxBulk: new(1)},
			},
			req: Request{
				Action:  ActionSilenceExpire,
				Tenants: []string{"prod-eu", "prod-us", "prod-us"},
				Lead:    "bulk expire",
			},
			want: Decision{Refusals: []Refusal{
				{Tenant: "prod-eu", Note: "denied", Message: "bulk expire denied on prod-eu: frozen"},
				{Tenant: "prod-us", Note: "max_bulk 1 exceeded", Message: "bulk expire on prod-us: 2 targets exceed max_bulk 1"},
			}},
		},
		{
			name: "the same request without lead keeps every outcome but the wording",
			set: Set{
				{Tenants: []string{"prod-eu"}, Deny: true, Reason: "frozen"},
				{Tenants: []string{"prod-us"}, MaxBulk: new(1)},
			},
			req: Request{
				Action:  ActionSilenceExpire,
				Tenants: []string{"prod-eu", "prod-us", "prod-us"},
			},
			want: Decision{Refusals: []Refusal{
				{Tenant: "prod-eu", Note: "denied", Message: "silence.expire denied on prod-eu: frozen"},
				{Tenant: "prod-us", Note: "max_bulk 1 exceeded", Message: "silence.expire on prod-us: 2 targets exceed max_bulk 1"},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.set.Decide(tt.req))
		})
	}
}

func TestDecisionReporting(t *testing.T) {
	t.Parallel()

	refused := Decision{Refusals: []Refusal{
		{Tenant: "prod-eu", Message: "first"},
		{Tenant: "prod-us", Message: "second"},
	}}

	tests := []struct {
		name         string
		d            Decision
		wantRefused  bool
		wantFlash    string
		wantMessages []string
	}{
		{name: "empty decision", d: Decision{}, wantRefused: false, wantFlash: "", wantMessages: nil},
		{
			name:         "refused decision",
			d:            refused,
			wantRefused:  true,
			wantFlash:    "first",
			wantMessages: []string{"first", "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.wantRefused, tt.d.Refused())
			require.Equal(t, tt.wantFlash, tt.d.Flash())
			if tt.wantMessages == nil {
				require.Empty(t, tt.d.Messages())
				return
			}
			require.Equal(t, tt.wantMessages, tt.d.Messages())
		})
	}
}

func TestDecisionTypedRefusals(t *testing.T) {
	t.Parallel()

	require.Empty(t, Decision{}.TypedRefusals())

	d := Decision{
		Refusals: []Refusal{{Tenant: "prod-eu", Note: "denied", Message: "silence.expire denied on prod-eu"}},
		Typed:    []string{"prod-eu", "prod-us"},
		Confirm:  ConfirmationTypeTenantName,
	}
	require.Equal(t, []Refusal{
		{Tenant: "prod-us", Note: "needs --confirm-tenant prod-us", Message: "prod-us requires --confirm-tenant prod-us"},
	}, d.TypedRefusals(), "a tenant already denied is named once, by the stronger refusal")
}

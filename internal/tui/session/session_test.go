// SPDX-License-Identifier: Apache-2.0

package session_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/session"
)

func decide(s *session.Session, tenant string, action guardrail.Action) guardrail.Decision {
	return s.Guardrails().Decide(guardrail.Request{Action: action, Tenants: []string{tenant}})
}

type policy struct {
	readOnly    bool
	concurrency int
	deniedProd  bool
	tenantURL   string
}

func observe(s *session.Session) policy {
	be, _ := s.TenantConfig("prod")
	return policy{
		readOnly:    s.ReadOnly(),
		concurrency: s.BulkConcurrency(),
		deniedProd:  decide(s, "prod", guardrail.ActionSilenceCreate).Refused(),
		tenantURL:   be.URL,
	}
}

var policyCases = []struct {
	name string
	cfg  config.Config
	want policy
}{
	{
		name: "zero config",
		want: policy{concurrency: config.DefaultBulkConcurrency},
	},
	{
		name: "defaults.read_only",
		cfg: config.Config{
			Defaults: config.Defaults{ReadOnly: true},
			Backends: []config.Backend{{Name: "prod", URL: "https://am"}},
		},
		want: policy{readOnly: true, concurrency: config.DefaultBulkConcurrency, tenantURL: "https://am"},
	},
	{
		name: "per-backend read_only",
		cfg: config.Config{Backends: []config.Backend{
			{Name: "prod", URL: "https://am", ReadOnly: true},
			{Name: "staging", URL: "https://am-staging"},
		}},
		want: policy{concurrency: config.DefaultBulkConcurrency, deniedProd: true, tenantURL: "https://am"},
	},
	{
		name: "a guardrail deny",
		cfg: config.Config{
			Backends:   []config.Backend{{Name: "prod", URL: "https://am"}},
			Guardrails: guardrail.Set{{Tenants: []string{"prod"}, Deny: true}},
		},
		want: policy{concurrency: config.DefaultBulkConcurrency, deniedProd: true, tenantURL: "https://am"},
	},
	{
		name: "bulk_concurrency set",
		cfg:  config.Config{Defaults: config.Defaults{BulkConcurrency: 3}},
		want: policy{concurrency: 3},
	},
}

func TestNewDerivesPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range policyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, observe(session.New(tc.cfg)))
		})
	}
}

// Apply over a session that started from the opposite policy, so a
// derivation Apply forgot to redo keeps the stale answer and fails.
func TestApplyRederivesPolicy(t *testing.T) {
	t.Parallel()

	stale := config.Config{
		Defaults:   config.Defaults{ReadOnly: true, BulkConcurrency: 9},
		Backends:   []config.Backend{{Name: "prod", URL: "https://stale", ReadOnly: true}},
		Guardrails: guardrail.Set{{Tenants: []string{"*"}, Deny: true}},
	}
	for _, tc := range policyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := session.New(stale)
			held := s.Config()
			s.Apply(tc.cfg)

			require.Equal(t, tc.want, observe(s))
			require.Same(t, held, s.Config(), "a holder of Config() must see the applied file")
			require.Equal(t, tc.cfg, *held)
		})
	}
}

func TestColumnsAndNotifyFollowApply(t *testing.T) {
	t.Parallel()

	s := session.New(config.Config{})
	next := config.Config{
		Pages: config.PageOverrides{
			Alerts:      config.AlertsPageConfig{Columns: []config.Column{{Label: "team"}}},
			GroupDetail: config.GroupDetailConfig{Columns: []config.Column{{Label: "pod"}}},
		},
		TUI: config.TUI{Notify: config.Notify{Enabled: true}},
	}
	s.Apply(next)

	require.Equal(t, next.Pages.Alerts.Columns, s.AlertColumns())
	require.Equal(t, next.Pages.GroupDetail.Columns, s.GroupDetailColumns())
	require.Equal(t, next.TUI.Notify, s.Notify())
}

// The page-wide switch still has one honest case: nothing writable
// anywhere. Anything short of that has to leave the bindings up, or
// a mixed fleet loses the keys its writable tenants accept.
func TestReadOnly_NothingWritable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "no backends at all", cfg: config.Config{}},
		{
			name: "one writable backend",
			cfg:  config.Config{Backends: []config.Backend{{Name: "prod"}}},
		},
		{
			name: "a read-only backend beside a writable one",
			cfg: config.Config{Backends: []config.Backend{
				{Name: "prod", ReadOnly: true}, {Name: "staging"},
			}},
		},
		{
			name: "every backend read-only",
			cfg: config.Config{Backends: []config.Backend{
				{Name: "prod", ReadOnly: true}, {Name: "staging", ReadOnly: true},
			}},
			want: true,
		},
		{
			name: "the global flag alone",
			cfg:  config.Config{Defaults: config.Defaults{ReadOnly: true}},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, session.New(tc.cfg).ReadOnly())
		})
	}
}

// configuration.md documents per-backend `read_only` as the first
// source of the read-only precedence, so every write verb has to
// refuse on that backend and none of them on its writable neighbour.
func TestGuardrails_DeniesEveryWriteVerbOnAReadOnlyBackend(t *testing.T) {
	t.Parallel()

	s := session.New(config.Config{Backends: []config.Backend{
		{Name: "prod", ReadOnly: true},
		{Name: "staging"},
	}})

	for _, action := range []guardrail.Action{
		guardrail.ActionSilenceCreate,
		guardrail.ActionSilenceUpdate,
		guardrail.ActionSilenceExpire,
		guardrail.ActionSilenceRecreate,
	} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			d := decide(s, "prod", action)
			require.True(t, d.Refused())
			require.Contains(t, d.Flash(), "prod", "the refusal names the tenant it refused")
			require.False(t, decide(s, "staging", action).Refused())
		})
	}
}

// A user rule and the read-only flag can deny the same tenant. The
// flag is the stronger statement, so its reason has to be the one the
// flash quotes rather than a rule's narrower wording.
func TestGuardrails_QuotesTheReadOnlyReasonOverAUserRule(t *testing.T) {
	t.Parallel()

	s := session.New(config.Config{
		Backends: []config.Backend{{Name: "prod", ReadOnly: true}},
		Guardrails: guardrail.Set{{
			Tenants: []string{"prod"},
			Deny:    true,
			Reason:  "ask the on-caller first",
		}},
	})

	d := decide(s, "prod", guardrail.ActionSilenceExpire)

	require.True(t, d.Refused())
	require.Equal(t, "silence.expire denied on prod: backend is read_only", d.Flash())
}

// The configured rules keep working beside the synthesized ones: a
// cap or a confirmation on a writable backend must survive the fold.
func TestGuardrails_KeepsTheConfiguredRules(t *testing.T) {
	t.Parallel()

	s := session.New(config.Config{
		Backends:   []config.Backend{{Name: "prod", ReadOnly: true}, {Name: "staging"}},
		Guardrails: guardrail.Set{{Tenants: []string{"staging"}, MaxBulk: new(3)}},
	})
	req := func(targets int) guardrail.Request {
		return guardrail.Request{
			Action:  guardrail.ActionSilenceCreate,
			Tenants: slices.Repeat([]string{"staging"}, targets),
		}
	}

	require.False(t, s.Guardrails().Decide(req(3)).Refused(), "the configured cap still fits at its own limit")
	require.Equal(t,
		"silence.create on staging: 4 targets exceed max_bulk 3",
		s.Guardrails().Decide(req(4)).Flash())
}

// A backend name is free-form while a rule's tenant list is a glob.
// An unescaped name carrying pattern syntax would match other
// backends or miss its own, and the deny would fail open on exactly
// the backend the user froze.
func TestGuardrails_DeniesABackendNamedWithGlobSyntax(t *testing.T) {
	t.Parallel()

	for _, name := range []string{`prod[1]`, `prod*`, `prod?`, `prod\x`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := session.New(config.Config{Backends: []config.Backend{{Name: name, ReadOnly: true}}})

			require.True(t, decide(s, name, guardrail.ActionSilenceCreate).Refused())
		})
	}
}

func TestTenantConfig_KeyedByBackendName(t *testing.T) {
	t.Parallel()

	s := session.New(config.Config{Backends: []config.Backend{
		{Name: "prod", URL: "http://am-prod"},
		{Name: "staging", URL: "http://am-staging"},
	}})

	prod, ok := s.TenantConfig("prod")
	require.True(t, ok)
	require.Equal(t, "http://am-prod", prod.URL)
	_, ok = s.TenantConfig("dev")
	require.False(t, ok)
}

func TestMust(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in        *session.Session
		wantPanic bool
	}{
		"nil panics":                 {wantPanic: true},
		"a given session comes back": {in: session.New(config.Config{})},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tt.wantPanic {
				require.Panics(t, func() { session.Must(tt.in) })
				return
			}
			require.Same(t, tt.in, session.Must(tt.in))
		})
	}
}

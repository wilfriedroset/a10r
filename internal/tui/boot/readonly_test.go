// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// configuration.md documents per-backend `read_only` as the first
// source of the read-only precedence, so every write verb has to
// refuse on that backend and none of them on its writable neighbour.
func TestWritePolicy_DeniesEveryWriteVerbOnAReadOnlyBackend(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Backends: []config.Backend{
		{Name: "prod", ReadOnly: true},
		{Name: "staging"},
	}}
	set := writePolicy(cfg)

	for _, action := range []string{
		guardrail.ActionSilenceCreate,
		guardrail.ActionSilenceUpdate,
		guardrail.ActionSilenceExpire,
		guardrail.ActionSilenceRecreate,
	} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			v := set.Evaluate("prod", action)
			require.True(t, v.Denied)
			require.Contains(t, v.DenyMessage(action, "prod"), "prod", "the refusal names the tenant it refused")
			require.False(t, set.Evaluate("staging", action).Denied)
		})
	}
}

// A user rule and the read-only flag can deny the same tenant. The
// flag is the stronger statement, so its reason has to be the one the
// flash quotes rather than a rule's narrower wording.
func TestWritePolicy_QuotesTheReadOnlyReasonOverAUserRule(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Backends: []config.Backend{{Name: "prod", ReadOnly: true}},
		Guardrails: guardrail.Set{{
			Tenants: []string{"prod"},
			Deny:    true,
			Reason:  "ask the on-caller first",
		}},
	}

	v := writePolicy(cfg).Evaluate("prod", guardrail.ActionSilenceExpire)

	require.True(t, v.Denied)
	require.Equal(t, "backend is read_only", v.Reason)
}

// The configured rules keep working beside the synthesized ones: a
// cap or a confirmation on a writable backend must survive the fold.
func TestWritePolicy_KeepsTheConfiguredRules(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Backends:   []config.Backend{{Name: "prod", ReadOnly: true}, {Name: "staging"}},
		Guardrails: guardrail.Set{{Tenants: []string{"staging"}, MaxBulk: 3}},
	}

	v := writePolicy(cfg).Evaluate("staging", guardrail.ActionSilenceCreate)

	require.False(t, v.Denied)
	require.Equal(t, 3, v.MaxBulk)
}

// A backend name is free-form while a rule's tenant list is a glob.
// An unescaped name carrying pattern syntax would compile to a
// pattern that never matches its own backend, and the deny would
// fail open on exactly the backend the user froze.
func TestWritePolicy_DeniesABackendNamedWithGlobSyntax(t *testing.T) {
	t.Parallel()

	for _, name := range []string{`prod[1]`, `prod*`, `prod?`, `prod\x`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{Backends: []config.Backend{{Name: name, ReadOnly: true}}}

			require.True(t, writePolicy(cfg).Evaluate(name, guardrail.ActionSilenceCreate).Denied)
		})
	}
}

// The page-wide switch still has one honest case: nothing writable
// anywhere. Anything short of that has to leave the bindings up, or
// a mixed fleet loses the keys its writable tenants accept.
func TestSessionReadOnly(t *testing.T) {
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
			want: false,
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

			require.Equal(t, tc.want, sessionReadOnly(&tc.cfg))
		})
	}
}

// The seam that matters: a page built by the factory has to carry the
// per-backend policy, because nothing between the config file and the
// page would otherwise enforce it.
func TestBuild_PerBackendReadOnlyReachesThePageEnv(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{
			{Name: "prod", URL: "https://am", ReadOnly: true},
			{Name: "staging", URL: "https://am-staging"},
		}}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	require.False(t, res.env.ReadOnly, "a writable tenant keeps the bindings up")
	require.True(t, res.env.Guardrails.Evaluate("prod", guardrail.ActionSilenceCreate).Denied)
	require.False(t, res.env.Guardrails.Evaluate("staging", guardrail.ActionSilenceCreate).Denied)
}

// A reload is the other way the flag moves, and the pages read the
// env rather than the config, so the derived policy has to be
// recomputed with everything else the reload refreshes.
func TestReload_AppliesAPerBackendReadOnlyFlipToThePolicy(t *testing.T) {
	t.Parallel()

	start := config.Config{Backends: []config.Backend{{Name: "prod", URL: "https://am"}}}
	r, env, _ := reloadFixture(t, start, func() (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{
			{Name: "prod", URL: "https://am", ReadOnly: true},
		}}, nil
	})

	require.IsType(t, app.ReloadedMsg{}, r.reload()())

	require.True(t, env.Guardrails.Evaluate("prod", guardrail.ActionSilenceExpire).Denied)
	require.True(t, env.ReadOnly, "the only backend is frozen, so the page-wide switch is honest")
}

// The end of the wire: a page the factory built must actually refuse
// the verb on a read-only backend, and keep it on the writable one
// beside it. Asserting on the env alone would pass with nothing
// downstream reading it.
func TestNewSilencesPage_RefusesAReadOnlyBackendPerRow(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{
			{Name: "prod", URL: "https://am", ReadOnly: true},
			{Name: "staging", URL: "https://am-staging"},
		}}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	page := newSilencesPage(res.env)
	// Distinct end times, because the page sorts on that column by
	// default and holds its rows per tenant in a map: equal keys would
	// leave the cursor on whichever tenant the map ranged first.
	for i, tenant := range []string{"prod", "staging"} {
		page, _ = page.Update(poll.DataMsg{Tenant: tenant, Resource: []backend.Silence{
			pagetest.Silence(pagetest.SilenceOptions{
				ID:        "sil-" + tenant,
				CreatedBy: "alice",
				State:     backend.SilenceStateActive,
				EndsIn:    time.Duration(i+1) * time.Hour,
			}),
		}})
	}

	require.True(t, guardedExpire(t, page), "the cursor sits on the read-only backend")

	_, cmd := page.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	flash, ok := cmd().(footer.FlashShowMsg)
	require.True(t, ok, "a refused press must say so")
	require.Contains(t, flash.Text, "denied on prod")

	page, _ = page.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.False(t, guardedExpire(t, page), "the writable backend keeps its verb")
}

func guardedExpire(t *testing.T, page app.Page) bool {
	t.Helper()
	for _, b := range page.Bindings() {
		if b.Key == "x" {
			return b.Guarded
		}
	}
	t.Fatal("the silences page must keep its expire row")
	return false
}

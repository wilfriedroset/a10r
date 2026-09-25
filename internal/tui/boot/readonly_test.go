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

// decide asks the policy about one target on one backend, the shape
// every read-only assertion here needs.
func decide(set guardrail.Set, tenant, action string) guardrail.Decision {
	return set.Decide(guardrail.Request{Action: action, Tenants: []string{tenant}})
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

	require.False(t, res.env.Session.ReadOnly(), "a writable tenant keeps the bindings up")
	require.True(t, decide(res.env.Session.Guardrails(), "prod", guardrail.ActionSilenceCreate).Refused())
	require.False(t, decide(res.env.Session.Guardrails(), "staging", guardrail.ActionSilenceCreate).Refused())
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

	require.True(t, decide(env.Session.Guardrails(), "prod", guardrail.ActionSilenceExpire).Refused())
	require.True(t, env.Session.ReadOnly(), "the only backend is frozen, so the page-wide switch is honest")
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

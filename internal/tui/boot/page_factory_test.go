// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
)

// TestNewSilencesPage_CarriesTheGuardrails pins the wiring boot owns.
// The silences page is where four of the guarded verbs live, so a page
// built here with an empty rule set makes every one of them fail open
// in the real app with nothing to signal it.
func TestNewSilencesPage_CarriesTheGuardrails(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Guardrails: guardrail.Set{{
			Tenants: []string{"prod"},
			Actions: []string{guardrail.ActionSilenceExpire},
			Deny:    true,
		}}}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	page := newSilencesPage(res.env)
	page, _ = page.Update(poll.DataMsg{Tenant: "prod", Resource: []backend.Silence{
		pagetest.Silence(pagetest.SilenceOptions{
			ID: "sil-1", CreatedBy: "alice", State: backend.SilenceStateActive, EndsIn: time.Hour,
		}),
	}})

	for _, b := range page.Bindings() {
		if b.Key == "x" {
			require.True(t, b.Guarded, "the expire verb reads the configured rules")
			return
		}
	}
	t.Fatal("the silences page must keep its expire row")
}

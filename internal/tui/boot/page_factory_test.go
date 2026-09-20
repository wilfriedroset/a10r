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
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
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

// TestNewInfoPage_RendersTheLiveReport pins the seam item 16 adds:
// `:info` must report the process the operator is in, not a rebuilt
// guess. Version, alias count, and backend list all arrive from
// different boot stages, so a frame carrying all three proves the
// wiring rather than the renderer, which internal/report already pins.
func TestNewInfoPage_RendersTheLiveReport(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	deps.Version = "1.2.3"
	deps.Commit = "cafebabe"
	deps.Date = "2026-09-20"
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Backends: []config.Backend{{
			Name: "prod", URL: "https://alertmanager.example.com",
		}}}, nil
	}
	deps.LoadAliases = func(string) (config.AliasMap, error) {
		return config.AliasMap{"po": "alerts", "sl": "silences"}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	frame := testutil.StripStyle(newInfoPage(res.env).View(120, 40))

	require.Contains(t, frame, "a10r 1.2.3 commit=cafebabe built=2026-09-20")
	require.Contains(t, frame, "aliases:    2")
	require.Contains(t, frame, "backends (1):")
	require.Contains(t, frame, "prod")
}

// The report is a closure, not a value captured at push time, so a
// scope the user switches to mid-session reaches the next re-render.
// An eagerly-built report.InfoInput would keep the frame on the scope
// the process started with and quietly answer the wrong question.
func TestNewInfoPage_ReportsTheScopeChosenAfterStartup(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	deps := testDeps(t)
	deps.HistoryDir = func() (string, error) { return stateDir, nil }
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{
			Backends: []config.Backend{
				{Name: "prod", URL: "https://one.example.com"},
				{Name: "stg", URL: "https://two.example.com"},
			},
			TUI: config.TUI{Remember: true},
		}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	page := newInfoPage(res.env)
	require.NotContains(t, testutil.StripStyle(page.View(120, 40)), "(remembered)")

	res.store.SetScope("stg")
	page, _ = page.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})

	require.Contains(t, testutil.StripStyle(page.View(120, 40)), "scope:      stg (remembered)")
}

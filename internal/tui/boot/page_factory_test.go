// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/guardrail"
	"github.com/wilfriedroset/a10r/internal/report"
	"github.com/wilfriedroset/a10r/internal/tui/page/groupdetail"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
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
			Actions: []string{"silence.expire"},
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

// A start that warns about a skin must be able to say so from inside
// the TUI. The warning only ever reached the log file before, which
// is the one place the operator cannot open without quitting.
func TestNewConfigPage_ShowsTheStartupWarnings(t *testing.T) {
	// Not parallel: the theme loader warns through slog.Default(),
	// which Build reassigns, so a concurrent Build would collect this
	// warning into its own capture.

	deps := testDeps(t)
	deps.LoadStyles = func(name, _ string) (*theme.Styles, error) {
		slog.Warn("unknown skin; falling back to default", slog.String("requested", name))
		return testutil.LoadStyles(t), nil
	}
	deps.LoadConfig = func(config.LoadOpts) (*config.Config, error) {
		return &config.Config{Theme: config.Theme{Name: "nord"}}, nil
	}
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	frame := testutil.StripStyle(newConfigPage(res.env).View(120, 40))

	require.Contains(t, frame, "warnings (1):")
	require.Contains(t, frame, "unknown skin; falling back to default (requested=nord)")
}

// The page reports startup, not the whole session. Drop the window
// close in Build and every other test still passes while the section
// silently turns into a live warning feed.
func TestNewConfigPage_StopsCollectingWhenBuildReturns(t *testing.T) {
	// Not parallel, for the same reason as the test above.

	deps := testDeps(t)
	res, err := Build(t.Context(), &config.CLIFlags{}, deps)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	slog.Warn("a warning raised long after startup")

	frame := testutil.StripStyle(newConfigPage(res.env).View(120, 40))

	require.Contains(t, frame, "warnings (0):")
	require.NotContains(t, frame, "long after startup")
}

// The anchors are the reason the two sections are worth opening on a
// host with many drop-ins: each must land on its header whatever the
// source count above it is. The report is synthetic rather than built
// from Build, so the source list is long enough for the jump to
// matter and no concurrent test can log a warning into the count.
func TestNewConfigPage_AnchorsReachTheirSections(t *testing.T) {
	t.Parallel()

	sources := make([]config.Source, 0, 30)
	for i := range cap(sources) {
		sources = append(sources, config.Source{
			Kind: config.SourceDropIn,
			Path: fmt.Sprintf("/etc/a10r/config.d/%02d-drop-in.yaml", i),
		})
	}
	env := &pageEnv{ConfigReport: func() report.ConfigInput {
		return report.ConfigInput{Sources: sources, Warnings: []string{"a startup warning"}}
	}}

	tests := []struct {
		name string
		// keys start from the top of the report, so `p` is only
		// meaningful after something scrolled away from it.
		keys []rune
		want string
	}{
		{name: "w reaches the warnings", keys: []rune{'w'}, want: "warnings (1):"},
		{name: "p returns to the sources", keys: []rune{'w', 'p'}, want: "sources (30):"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			page := newConfigPage(env)
			for _, k := range tc.keys {
				page, _ = page.Update(tea.KeyPressMsg{Code: k, Text: string(k)})
			}

			require.Contains(t, testutil.StripStyle(page.View(120, 2)), tc.want)
		})
	}
}

type sortKeyRecorder struct{ keys []string }

func (r *sortKeyRecorder) Sort(resource string) string {
	r.keys = append(r.keys, resource)
	return ""
}

func (*sortKeyRecorder) SetSort(string, string) {}

// TestSortResources_CoverEveryPageSortKey pins the keep-list PruneSort
// reads against the keys the pages bind: a key missing from it gets
// its remembered sort wiped on every start, silently.
func TestSortResources_CoverEveryPageSortKey(t *testing.T) {
	t.Parallel()

	res, err := Build(t.Context(), &config.CLIFlags{}, testDeps(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, res.Close()) })

	rec := &sortKeyRecorder{}
	env := *res.env
	env.SortMemory = rec
	newAlertsPage(&env, "", "")
	newSilencesPage(&env)
	newReceiversPage(&env)
	newTenantPage(&env, nil)
	groupdetail.New(groupdetail.Options{Styles: env.Styles, Session: env.Session, SortMemory: rec})

	require.ElementsMatch(t, rec.keys, sortResources)
}

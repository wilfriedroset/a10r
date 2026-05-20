// SPDX-License-Identifier: Apache-2.0

package pagetest_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/page/pagetest"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func TestAlert_DefaultsCoverEverySpec(t *testing.T) {
	t.Parallel()

	a := pagetest.Alert(pagetest.AlertOptions{})
	require.NotEmpty(t, a.Labels["alertname"],
		"zero-option Alert must still carry an alertname so renderers don't panic on empty labels")
	require.NotEmpty(t, a.Labels["severity"],
		"zero-option Alert must carry a severity label so severity sorts pick it up")
	require.Equal(t, backend.AlertStateActive, a.State,
		"default state is active — the most exercised path")
	require.False(t, a.StartsAt.IsZero(),
		"default StartsAt must be non-zero so age column renders something")
}

func TestAlert_OptionsOverrideDefaults(t *testing.T) {
	t.Parallel()

	a := pagetest.Alert(pagetest.AlertOptions{
		Name:        "HighCPU",
		Severity:    "critical",
		State:       backend.AlertStateSuppressed,
		Now:         fixedNow,
		Age:         5 * time.Minute,
		Fingerprint: "abc123",
		Labels:      map[string]string{"instance": "host-1"},
		Annotations: map[string]string{"summary": "hot"},
		SilencedBy:  []string{"sil-1"},
	})

	require.Equal(t, "HighCPU", a.Labels["alertname"])
	require.Equal(t, "critical", a.Labels["severity"])
	require.Equal(t, "host-1", a.Labels["instance"],
		"extra Labels must merge with the alertname/severity defaults, not replace them")
	require.Equal(t, "hot", a.Annotations["summary"])
	require.Equal(t, backend.AlertStateSuppressed, a.State)
	require.Equal(t, fixedNow.Add(-5*time.Minute), a.StartsAt,
		"Age subtracts from Now so tests get deterministic StartsAt without manual arithmetic")
	require.Equal(t, "abc123", a.Fingerprint)
	require.Equal(t, []string{"sil-1"}, a.SilencedBy)
}

func TestSilence_DefaultsCoverEverySpec(t *testing.T) {
	t.Parallel()

	s := pagetest.Silence(pagetest.SilenceOptions{})
	require.NotEmpty(t, s.ID, "default ID must be non-empty so renderers index reliably")
	require.NotEmpty(t, s.CreatedBy, "default CreatedBy must be non-empty so the column renders something")
	require.Equal(t, backend.SilenceStateActive, s.State)
	require.False(t, s.StartsAt.IsZero())
	require.False(t, s.EndsAt.IsZero())
}

func TestSilence_OptionsOverrideDefaults(t *testing.T) {
	t.Parallel()

	s := pagetest.Silence(pagetest.SilenceOptions{
		ID:        "sil-7",
		CreatedBy: "alice",
		State:     backend.SilenceStatePending,
		Now:       fixedNow,
		StartsIn:  -time.Hour,
		EndsIn:    2 * time.Hour,
		Comment:   "scheduled maintenance",
	})

	require.Equal(t, "sil-7", s.ID)
	require.Equal(t, "alice", s.CreatedBy)
	require.Equal(t, backend.SilenceStatePending, s.State)
	require.Equal(t, fixedNow.Add(-time.Hour), s.StartsAt,
		"StartsIn is relative to Now — negative values land in the past")
	require.Equal(t, fixedNow.Add(2*time.Hour), s.EndsAt)
	require.Equal(t, "scheduled maintenance", s.Comment)
}

func TestGroup_DefaultsAndOverrides(t *testing.T) {
	t.Parallel()

	g := pagetest.Group(pagetest.GroupOptions{})
	require.NotEmpty(t, g.Labels,
		"default Group must have at least one label so commonLabels has something to chew on")

	g = pagetest.Group(pagetest.GroupOptions{
		Labels: map[string]string{"team": "platform"},
		Alerts: []backend.Alert{
			pagetest.Alert(pagetest.AlertOptions{Name: "A", Severity: "critical"}),
			pagetest.Alert(pagetest.AlertOptions{Name: "B", Severity: "warning"}),
		},
	})
	require.Equal(t, "platform", g.Labels["team"])
	require.Len(t, g.Alerts, 2)
	require.Equal(t, "A", g.Alerts[0].Labels["alertname"])
	require.Equal(t, "B", g.Alerts[1].Labels["alertname"])
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func TestToGroupRow_DedupsAndSortsReceivers(t *testing.T) {
	t.Parallel()

	got := toGroupRow("prod", backend.AlertGroup{
		Labels: map[string]string{"alertname": "HighCPU", "team": "plat"},
		Alerts: []backend.Alert{
			{Receivers: []string{"pager", "team-ops"}},
			{Receivers: []string{"pager", "email"}}, // pager dups, email is new.
			{Receivers: []string{""}},               // empty entry must be skipped.
		},
	})
	require.Equal(t, "prod", got.Tenant)
	require.Equal(t, 3, got.Count)
	require.Equal(t, []string{"email", "pager", "team-ops"}, got.Receivers,
		"receivers must dedup and sort for stable output")
	require.Equal(t, "HighCPU", got.Labels["alertname"])
}

func TestFilterGroupRows_ByReceiverCaseInsensitive(t *testing.T) {
	t.Parallel()

	rows := []groupRow{
		{Labels: map[string]string{"alertname": "A"}, Receivers: []string{"pager", "email"}},
		{Labels: map[string]string{"alertname": "B"}, Receivers: []string{"slack"}},
		{Labels: map[string]string{"alertname": "C"}, Receivers: []string{"PAGER"}},
	}
	got := filterGroupRows(rows, "Pager")
	require.Len(t, got, 2)
	require.Equal(t, "A", got[0].Labels["alertname"])
	require.Equal(t, "C", got[1].Labels["alertname"])
}

func TestFilterGroupRows_NoFilterIsIdentity(t *testing.T) {
	t.Parallel()

	rows := []groupRow{
		{Labels: map[string]string{"x": "1"}},
		{Labels: map[string]string{"x": "2"}},
	}
	got := filterGroupRows(rows, "")
	require.Len(t, got, 2)
}

func TestSortGroupRows_TenantThenLabelsThenCount(t *testing.T) {
	t.Parallel()

	rows := []groupRow{
		{Tenant: "staging", Labels: map[string]string{"a": "1"}, Count: 3},
		{Tenant: "prod", Labels: map[string]string{"b": "2"}, Count: 1},
		{Tenant: "prod", Labels: map[string]string{"a": "1"}, Count: 5},
		{Tenant: "prod", Labels: map[string]string{"a": "1"}, Count: 2},
	}
	sortGroupRows(rows)
	require.Equal(t, "prod", rows[0].Tenant)
	require.Equal(t, "a=1", summariseLabels(rows[0].Labels))
	require.Equal(t, 2, rows[0].Count)
	require.Equal(t, 5, rows[1].Count)
	require.Equal(t, "b=2", summariseLabels(rows[2].Labels))
	require.Equal(t, "staging", rows[3].Tenant)
}

func TestSummariseLabels_DeterministicKeyOrder(t *testing.T) {
	t.Parallel()

	// Map iteration is randomised; the helper must walk keys in
	// sort order so the rendered output is reproducible.
	got := summariseLabels(map[string]string{"team": "plat", "alertname": "HighCPU"})
	require.Equal(t, "alertname=HighCPU,team=plat", got)
}

func TestSummariseLabels_EmptyIsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, summariseLabels(nil))
	require.Empty(t, summariseLabels(map[string]string{}))
}

func TestRenderGroupRows_TableHeaderAndCells(t *testing.T) {
	t.Parallel()

	rows := []groupRow{
		{
			Tenant:    "prod",
			Labels:    map[string]string{"alertname": "HighCPU"},
			Count:     3,
			Receivers: []string{"pager"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, renderGroupTable(&buf, rows))
	out := buf.String()
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "LABELS")
	require.Contains(t, out, "COUNT")
	require.Contains(t, out, "RECEIVERS")
	require.Contains(t, out, "alertname=HighCPU")
	require.Contains(t, out, "pager")
	require.Contains(t, out, "3")
}

func TestRenderGroupRows_JSONIncludesLabelsMap(t *testing.T) {
	t.Parallel()

	rows := []groupRow{
		{
			Tenant: "prod",
			Labels: map[string]string{"team": "plat"},
			Count:  1,
		},
	}
	var buf bytes.Buffer
	require.NoError(t, renderGroupJSON(&buf, rows))
	out := buf.String()
	require.Contains(t, out, `"tenant": "prod"`)
	require.Contains(t, out, `"team": "plat"`,
		"labels map round-trips through JSON output")
}

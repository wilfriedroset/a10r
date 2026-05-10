// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/output"
)

func TestToAlertRow_PullsLabels(t *testing.T) {
	t.Parallel()

	got := toAlertRow("prod", backend.Alert{
		Fingerprint: "abc",
		State:       backend.AlertStateActive,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical", "team": "plat"},
	})
	require.Equal(t, "prod", got.Tenant)
	require.Equal(t, "HighCPU", got.Name)
	require.Equal(t, "critical", got.Severity)
	require.Equal(t, "abc", got.Fingerprint)
	require.Equal(t, backend.AlertStateActive, got.State)
}

func TestFilterAlertRows_BySeverityCaseInsensitive(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Name: "A", Severity: "critical"},
		{Name: "B", Severity: "warning"},
		{Name: "C", Severity: "Critical"},
	}
	got := filterAlertRows(rows, "CRITICAL", "")
	require.Len(t, got, 2)
	require.Equal(t, "A", got[0].Name)
	require.Equal(t, "C", got[1].Name)
}

func TestFilterAlertRows_ByState(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Name: "A", State: backend.AlertStateActive},
		{Name: "B", State: backend.AlertStateSuppressed},
	}
	got := filterAlertRows(rows, "", "active")
	require.Len(t, got, 1)
	require.Equal(t, "A", got[0].Name)
}

func TestFilterAlertRows_NoFiltersIsIdentity(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Name: "A", Severity: "x"},
		{Name: "B", Severity: "y"},
	}
	got := filterAlertRows(rows, "", "")
	require.Len(t, got, 2)
}

func TestSortAlertRows_TenantThenNameThenFingerprint(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Tenant: "staging", Name: "B", Fingerprint: "1"},
		{Tenant: "prod", Name: "B", Fingerprint: "2"},
		{Tenant: "prod", Name: "A", Fingerprint: "3"},
		{Tenant: "prod", Name: "B", Fingerprint: "1"},
	}
	sortAlertRows(rows)
	require.Equal(t, "prod", rows[0].Tenant)
	require.Equal(t, "A", rows[0].Name)
	require.Equal(t, "prod", rows[1].Tenant)
	require.Equal(t, "B", rows[1].Name)
	require.Equal(t, "1", rows[1].Fingerprint)
	require.Equal(t, "prod", rows[2].Tenant)
	require.Equal(t, "B", rows[2].Name)
	require.Equal(t, "2", rows[2].Fingerprint)
	require.Equal(t, "staging", rows[3].Tenant)
}

func TestRenderAlertRows_TableHeaderAndCells(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Tenant: "prod", Name: "HighCPU", Severity: "critical", State: backend.AlertStateActive},
	}
	var buf bytes.Buffer
	require.NoError(t, renderAlertRows(&buf, rows, output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "HighCPU")
	require.Contains(t, out, "critical")
}

func TestRenderAlertRows_JSONIncludesLabels(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{
			Tenant: "prod", Name: "HighCPU", Severity: "critical",
			State:  backend.AlertStateActive,
			Labels: map[string]string{"team": "plat"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, renderAlertRows(&buf, rows, output.FormatJSON))
	out := buf.String()
	require.Contains(t, out, `"tenant": "prod"`)
	require.Contains(t, out, `"severity": "critical"`)
	require.Contains(t, out, `"team": "plat"`,
		"labels map round-trips through the JSON output")
}

func TestAlertTableRows_OrderMatchesCols(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Tenant: "prod", Name: "HighCPU", Severity: "critical", State: backend.AlertStateActive},
	}
	got := alertTableRows(rows)
	require.Len(t, got, 1)
	require.Equal(t, []string{"prod", "HighCPU", "critical", "active"}, got[0])
}

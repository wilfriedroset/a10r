// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/listcmd"
)

func TestValidateAlertState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty is no-op", in: "", want: ""},
		{name: "active", in: "active", want: "active"},
		{name: "mixed case folds", in: "Suppressed", want: "suppressed"},
		{name: "unprocessed", in: "unprocessed", want: "unprocessed"},
		{name: "trim", in: "  active  ", want: "active"},
		// A silences-state value is not an alert state — guards against
		// the typo that previously matched nothing and exited 0.
		{name: "pending is not an alert state", in: "pending", wantErr: true},
		{name: "typo fails closed", in: "activ", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateAlertState(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestRunAlertsList_RejectsBadStateBeforeFanOut locks that the --state
// gate runs ahead of any backend work: a bogus state errors even with a
// config that would otherwise be loaded and fanned out.
func TestRunAlertsList_RejectsBadStateBeforeFanOut(t *testing.T) {
	t.Parallel()

	err := runAlertsList(context.Background(), &bytes.Buffer{}, &GlobalFlags{},
		alertsListOptions{State: "activ"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown state")
}

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
		{Tenant: "prod", Fingerprint: "3b15fd163d70e9cc", Name: "HighCPU", Severity: "critical", State: backend.AlertStateActive},
	}
	var buf bytes.Buffer
	require.NoError(t, renderAlertTable(&buf, rows))
	out := buf.String()
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "FINGERPRINT")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "HighCPU")
	require.Contains(t, out, "critical")
	require.Contains(t, out, "3b15fd163d70e9cc",
		"fingerprint is shown so `alerts get <fingerprint>` is discoverable from the list")
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
	require.NoError(t, listcmd.JSONRenderer(&buf, rows))
	out := buf.String()
	require.Contains(t, out, `"tenant": "prod"`)
	require.Contains(t, out, `"severity": "critical"`)
	require.Contains(t, out, `"team": "plat"`,
		"labels map round-trips through the JSON output")
}

func TestAlertTableRows_OrderMatchesCols(t *testing.T) {
	t.Parallel()

	rows := []alertRow{
		{Tenant: "prod", Fingerprint: "3b15fd163d70e9cc", Name: "HighCPU", Severity: "critical", State: backend.AlertStateActive},
	}
	got := alertTableRows(rows)
	require.Len(t, got, 1)
	require.Equal(t, []string{"prod", "3b15fd163d70e9cc", "HighCPU", "critical", "active"}, got[0])
}

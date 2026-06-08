// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/backendtest"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
)

// alertsGetClient is a fake backend.Client returning a fixed alert
// list (or error) from ListAlerts; every other method falls through to
// ClientStub's ErrUnsupported.
type alertsGetClient struct {
	backendtest.ClientStub
	alerts []backend.Alert
	err    error
}

func (c alertsGetClient) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	return c.alerts, c.err
}

// buildFactory builds a ClientFactory + config over the named fakes so
// alertGet's fan-out runs without network.
func buildFactory(clients map[string]backend.Client) (*config.Config, listcmd.ClientFactory) {
	names := []config.Backend{{Name: "prod"}, {Name: "staging"}}
	return &config.Config{Backends: names},
		func(be config.Backend) (backend.Client, error) {
			return clients[be.Name], nil
		}
}

func TestAlertGet_FoundRendersDetail(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod": alertsGetClient{alerts: []backend.Alert{{
			Fingerprint: "abc123",
			State:       backend.AlertStateActive,
			Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		}}},
		"staging": alertsGetClient{alerts: nil},
	})

	var out, errOut bytes.Buffer
	err := alertGet(context.Background(), &out, &errOut, cfg, build, "abc123", "json")
	require.NoError(t, err)

	var got alertDetail
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, "prod", got.Tenant)
	require.Equal(t, "abc123", got.Fingerprint)
	require.Equal(t, "HighCPU", got.Labels["alertname"])
}

func TestAlertGet_NotFoundExits5(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod":    alertsGetClient{alerts: nil},
		"staging": alertsGetClient{alerts: nil},
	})

	var out, errOut bytes.Buffer
	err := alertGet(context.Background(), &out, &errOut, cfg, build, "missing", "json")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
}

func TestAlertGet_AllBackendsFailExitsUnreachable(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod":    alertsGetClient{err: errors.New("dial tcp: refused")},
		"staging": alertsGetClient{err: errors.New("dial tcp: refused")},
	})

	var out, errOut bytes.Buffer
	err := alertGet(context.Background(), &out, &errOut, cfg, build, "abc123", "json")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitUnreachable, ex.Code)
}

func TestAlertGet_PartialFailureStillRendersMatch(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod": alertsGetClient{err: errors.New("dial tcp: refused")},
		"staging": alertsGetClient{alerts: []backend.Alert{{
			Fingerprint: "abc123",
			State:       backend.AlertStateActive,
			Labels:      map[string]string{"alertname": "HighCPU"},
		}}},
	})

	var out, errOut bytes.Buffer
	err := alertGet(context.Background(), &out, &errOut, cfg, build, "abc123", "json")
	require.NoError(t, err, "a match on one backend wins despite another failing")

	var got alertDetail
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, "staging", got.Tenant)
	require.Contains(t, errOut.String(), `backend "prod"`,
		"the failed backend is reported on stderr")
}

func TestAlertGet_MultiTenantMatchRendersSequence(t *testing.T) {
	t.Parallel()

	mirrored := []backend.Alert{{
		Fingerprint: "abc123",
		State:       backend.AlertStateActive,
		Labels:      map[string]string{"alertname": "HighCPU"},
	}}
	cfg, build := buildFactory(map[string]backend.Client{
		"prod":    alertsGetClient{alerts: mirrored},
		"staging": alertsGetClient{alerts: mirrored},
	})

	var out, errOut bytes.Buffer
	err := alertGet(context.Background(), &out, &errOut, cfg, build, "abc123", "json")
	require.NoError(t, err)

	var got []alertDetail
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "prod", got[0].Tenant)
	require.Equal(t, "staging", got[1].Tenant)
}

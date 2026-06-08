// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/backendtest"
	"github.com/wilfriedroset/a10r/internal/config"
)

var testNow = time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

func TestParseSilenceStart(t *testing.T) {
	t.Parallel()

	got, err := parseSilenceStart("", testNow)
	require.NoError(t, err)
	require.True(t, got.Equal(testNow), "empty means now")

	got, err = parseSilenceStart("now", testNow)
	require.NoError(t, err)
	require.True(t, got.Equal(testNow), "now means now")

	got, err = parseSilenceStart("2026-06-08T15:00:00Z", testNow)
	require.NoError(t, err)
	require.True(t, got.Equal(time.Date(2026, 6, 8, 15, 0, 0, 0, time.UTC)))

	_, err = parseSilenceStart("soon", testNow)
	require.Error(t, err)
}

func TestParseSilenceEnd(t *testing.T) {
	t.Parallel()

	got, err := parseSilenceEnd("2h", testNow)
	require.NoError(t, err)
	require.True(t, got.Equal(testNow.Add(2*time.Hour)), "duration is relative to start")

	got, err = parseSilenceEnd("7d2h", testNow)
	require.NoError(t, err)
	require.True(t, got.Equal(testNow.Add(7*24*time.Hour+2*time.Hour)))

	got, err = parseSilenceEnd("2026-06-09T00:00:00Z", testNow)
	require.NoError(t, err)
	require.True(t, got.Equal(time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC)), "rfc3339 is absolute")

	_, err = parseSilenceEnd("whenever", testNow)
	require.Error(t, err)
}

// silenceWriteClient fakes the silence write + read surface: ListAlerts
// feeds the --alert resolution, CreateSilence records the spec and
// returns a deterministic id, and createErr (when set) fails the write.
type silenceWriteClient struct {
	backendtest.ClientStub
	alerts    []backend.Alert
	listErr   error
	createID  string
	createErr error
	created   *backend.SilenceSpec
}

func (c *silenceWriteClient) ListAlerts(context.Context, backend.AlertFilter) ([]backend.Alert, error) {
	return c.alerts, c.listErr
}

func (c *silenceWriteClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	if c.createErr != nil {
		return "", c.createErr
	}
	c.created = &spec
	return c.createID, nil
}

func cfgWith(backends ...config.Backend) *config.Config {
	return &config.Config{Backends: backends}
}

func TestSilenceCreate_MatcherSingleTenant(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{createID: "new-1"}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{
			Matchers: []string{`severity="critical"`},
			Ends:     "2h",
			Comment:  "maint",
		}, "alice")
	require.NoError(t, err)
	require.Equal(t, "prod\tnew-1\n", out.String())
	require.NotNil(t, client.created)
	require.Equal(t, "alice", client.created.CreatedBy)
	require.Equal(t, "maint", client.created.Comment)
	require.Len(t, client.created.Matchers, 1)
	require.Equal(t, "severity", client.created.Matchers[0].Name)
	require.True(t, client.created.EndsAt.Equal(testNow.Add(2*time.Hour)))
}

func TestSilenceCreate_MatcherMultiTenantNeedsExplicitTenant(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"}, config.Backend{Name: "staging"})
	build := func(config.Backend) (backend.Client, error) { return &silenceWriteClient{}, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, false,
		silenceCreateOptions{Matchers: []string{`severity="critical"`}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--tenant")
}

func TestSilenceCreate_MatcherFanOutWhenTenantExplicit(t *testing.T) {
	t.Parallel()

	prod := &silenceWriteClient{createID: "p1"}
	staging := &silenceWriteClient{createID: "s1"}
	cfg := cfgWith(config.Backend{Name: "prod"}, config.Backend{Name: "staging"})
	build := func(be config.Backend) (backend.Client, error) {
		if be.Name == "prod" {
			return prod, nil
		}
		return staging, nil
	}

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{Matchers: []string{`severity="critical"`}, Ends: "2h", Comment: "m"}, "alice")
	require.NoError(t, err)
	require.Equal(t, "prod\tp1\nstaging\ts1\n", out.String())
}

func TestSilenceCreate_AlertDerivesMatchersFromLabels(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{
		createID: "a1",
		alerts: []backend.Alert{{
			Fingerprint: "fp1",
			Labels:      map[string]string{"__name__": "ALERTS", "alertname": "HighCPU", "instance": "h1"},
		}},
	}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, false,
		silenceCreateOptions{Alerts: []string{"fp1"}, Ends: "2h", Comment: "m"}, "alice")
	require.NoError(t, err)
	require.NotNil(t, client.created)
	// __name__ dropped, sorted: alertname, instance
	require.Len(t, client.created.Matchers, 2)
	require.Equal(t, "alertname", client.created.Matchers[0].Name)
	require.Equal(t, "instance", client.created.Matchers[1].Name)
}

func TestSilenceCreate_AlertNotFoundExits5(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{alerts: nil}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, false,
		silenceCreateOptions{Alerts: []string{"ghost"}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
}

func TestSilenceCreate_AlertPartialMissAbortsNothingWritten(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{
		createID: "a1",
		alerts: []backend.Alert{{
			Fingerprint: "fp1",
			Labels:      map[string]string{"alertname": "HighCPU"},
		}},
	}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, false,
		silenceCreateOptions{Alerts: []string{"fp1", "fp2"}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
	require.Contains(t, err.Error(), "fp2")
	require.Nil(t, client.created, "one missing fingerprint aborts the whole request; nothing is written")
}

func TestSilenceCreate_AlertMissWhileBackendUnreachableIsUnreachable(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) {
		return &silenceWriteClient{listErr: context.DeadlineExceeded}, nil
	}

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, false,
		silenceCreateOptions{Alerts: []string{"fp1"}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitUnreachable, ex.Code, "a miss behind an unreachable backend signals retry, not absence")
}

func TestSilenceCreate_AlertMirroredAcrossTenants(t *testing.T) {
	t.Parallel()

	mirrored := []backend.Alert{{
		Fingerprint: "fp1",
		Labels:      map[string]string{"alertname": "HighCPU", "instance": "h1"},
	}}
	prod := &silenceWriteClient{createID: "p1", alerts: mirrored}
	staging := &silenceWriteClient{createID: "s1", alerts: mirrored}
	cfg := cfgWith(config.Backend{Name: "prod"}, config.Backend{Name: "staging"})
	build := func(be config.Backend) (backend.Client, error) {
		if be.Name == "prod" {
			return prod, nil
		}
		return staging, nil
	}

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, false,
		silenceCreateOptions{Alerts: []string{"fp1"}, Ends: "2h", Comment: "m"}, "alice")
	require.NoError(t, err)
	require.Equal(t, "prod\tp1\nstaging\ts1\n", out.String())
	require.NotNil(t, prod.created)
	require.NotNil(t, staging.created)
	// independent matcher slices, no cross-tenant aliasing
	require.NotSame(t, &prod.created.Matchers, &staging.created.Matchers)
	require.Equal(t, prod.created.Matchers, staging.created.Matchers)
}

func TestSilenceCreate_GlobalReadOnlyFailsFast(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{createID: "x"}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, true, build, testNow, true,
		silenceCreateOptions{Matchers: []string{`a="b"`}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-only")
	require.Nil(t, client.created, "no silence written under global read-only")
}

func TestSilenceCreate_ReadOnlyTargetFailsClosed(t *testing.T) {
	t.Parallel()

	prod := &silenceWriteClient{createID: "p1"}
	staging := &silenceWriteClient{createID: "s1"}
	cfg := cfgWith(config.Backend{Name: "prod"}, config.Backend{Name: "staging", ReadOnly: true})
	build := func(be config.Backend) (backend.Client, error) {
		if be.Name == "prod" {
			return prod, nil
		}
		return staging, nil
	}

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{Matchers: []string{`a="b"`}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "staging")
	require.Nil(t, prod.created, "fail-closed: NO silence written anywhere when one target is read-only")
	require.Nil(t, staging.created)
}

func TestSilenceCreate_RequiresComment(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return &silenceWriteClient{}, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{Matchers: []string{`a="b"`}, Ends: "2h"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "comment")
}

func TestSilenceCreate_MatcherAndAlertMutuallyExclusive(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return &silenceWriteClient{}, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{Matchers: []string{`a="b"`}, Alerts: []string{"fp1"}, Ends: "2h", Comment: "m"}, "alice")
	require.Error(t, err)
}

func TestSilenceCreate_WriteFailureReportedNonZero(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{createErr: context.DeadlineExceeded}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{Matchers: []string{`a="b"`}, Ends: "2h", Comment: "m", Output: "json"}, "alice")
	require.Error(t, err)

	var got []writeResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "error", got[0].Status)
}

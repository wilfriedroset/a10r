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
)

// silenceExpireClient fakes the list + expire surface: ListSilences
// feeds id resolution, ExpireSilence records the ids it received.
type silenceExpireClient struct {
	backendtest.ClientStub
	silences []backend.Silence
	listErr  error
	expired  []string
	expErr   error
}

func (c *silenceExpireClient) ListSilences(context.Context, backend.SilenceFilter) ([]backend.Silence, error) {
	return c.silences, c.listErr
}

func (c *silenceExpireClient) ExpireSilence(_ context.Context, id string) error {
	if c.expErr != nil {
		return c.expErr
	}
	c.expired = append(c.expired, id)
	return nil
}

func activeS(id string) backend.Silence {
	return backend.Silence{ID: id, State: backend.SilenceStateActive}
}

func TestSilenceExpire_OneID(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", false)
	require.NoError(t, err)
	require.Equal(t, "prod\tsil-1\n", out.String())
	require.Equal(t, "recreate with: a10r silences recreate sil-1\n", errOut.String(),
		"a single expire emits the recreate undo hint")
	require.Equal(t, []string{"sil-1"}, client.expired)
}

func TestSilenceExpire_MultipleIDs(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1"), activeS("sil-2")}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1", "sil-2"}, "", false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sil-1", "sil-2"}, client.expired)
	require.Empty(t, errOut.String(), "multi-id expire suppresses the recreate hint")
}

func TestSilenceExpire_NotFoundIDIsPerIDFailureLenient(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1", "ghost"}, "", false)
	require.Error(t, err, "a missing id makes the command exit non-zero")
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, []string{"sil-1"}, client.expired, "the present id is still expired (lenient per-id)")
	require.Equal(t, "prod\tsil-1\n", out.String())
	require.Contains(t, errOut.String(), "ghost")
}

// TestSilenceExpire_AllNotFoundExits5 locks the not-found contract: when
// no requested id resolves anywhere (and a backend answered), expire
// exits 5 like get/update/recreate rather than the lenient per-id 1.
func TestSilenceExpire_AllNotFoundExits5(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: nil}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"ghost"}, "", false)
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
	require.Empty(t, client.expired)
}

// TestSilenceExpire_AllNotFoundUnreachable: when nothing resolves and a
// backend failed, the miss is "could not confirm" → ExitUnreachable.
func TestSilenceExpire_AllNotFoundUnreachable(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{listErr: context.DeadlineExceeded}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"ghost"}, "", false)
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitUnreachable, ex.Code)
}

// TestSilenceExpire_PartialJSONCarriesIDNoTenant: a partial result (one
// id found, one missing) still renders the structured array, and the
// missing id's record carries the id with an empty tenant.
func TestSilenceExpire_PartialJSONCarriesIDNoTenant(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1", "ghost"}, "json", false)
	require.Error(t, err)

	var got []writeResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 2)
	var ghost writeResult
	for _, r := range got {
		if r.ID == "ghost" {
			ghost = r
		}
	}
	require.Equal(t, "ghost", ghost.ID)
	require.Empty(t, ghost.Tenant)
	require.Equal(t, "error", ghost.Status)
}

func TestSilenceExpire_AlreadyExpiredReportedNonZero(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{{ID: "sil-1", State: backend.SilenceStateExpired}}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", false)
	require.Error(t, err)
	require.Empty(t, client.expired, "an already-expired silence is not re-expired")
	require.Contains(t, errOut.String(), "already expired")
}

func TestSilenceExpire_ReadOnlyTargetFailsClosed(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod", ReadOnly: true})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "prod")
	require.Empty(t, client.expired)
}

func TestSilenceExpire_GlobalReadOnlyFailsClosed(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, true, build, []string{"sil-1"}, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-only")
	require.Empty(t, client.expired)
}

func TestSilenceExpire_ExpireRPCFailureReported(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}, expErr: errors.New("boom")}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", false)
	require.Error(t, err)
	require.Contains(t, errOut.String(), "boom")
}

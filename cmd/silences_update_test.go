// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/backendtest"
	"github.com/wilfriedroset/a10r/internal/config"
)

// silenceMutateClient fakes the get-by-id + update/expire surface: a hit
// returns the seeded silence, a miss returns ErrNotFound; UpdateSilence
// records the spec it received.
type silenceMutateClient struct {
	backendtest.ClientStub
	silence  backend.Silence
	hit      bool
	getErr   error
	updated  *backend.SilenceSpec
	updateID string
}

func (c *silenceMutateClient) GetSilence(context.Context, string) (backend.Silence, error) {
	if c.getErr != nil {
		return backend.Silence{}, c.getErr
	}
	if !c.hit {
		return backend.Silence{}, backend.ErrNotFound
	}
	return c.silence, nil
}

func (c *silenceMutateClient) UpdateSilence(_ context.Context, id string, spec backend.SilenceSpec) error {
	c.updated = &spec
	c.updateID = id
	return nil
}

func activeSilence() backend.Silence {
	return backend.Silence{
		ID:        "sil-1",
		State:     backend.SilenceStateActive,
		CreatedBy: "alice",
		Comment:   "maint",
		StartsAt:  testNow.Add(-time.Hour),
		EndsAt:    testNow.Add(time.Hour),
		Matchers:  []backend.Matcher{{Name: "severity", Value: "critical", IsEqual: true}},
	}
}

func TestSilenceUpdate_PatchEndsKeepsRest(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Ends: "4h"}, "")
	require.NoError(t, err)
	require.Equal(t, "prod\tsil-1\n", out.String())
	require.NotNil(t, client.updated)
	// ends repatched relative to the existing start; everything else kept
	require.True(t, client.updated.EndsAt.Equal(client.updated.StartsAt.Add(4*time.Hour)))
	require.Equal(t, "maint", client.updated.Comment)
	require.Equal(t, "alice", client.updated.CreatedBy)
	require.Len(t, client.updated.Matchers, 1)
	require.Equal(t, "sil-1", client.updateID, "the id is stable across an update")
}

func TestSilenceUpdate_MirroredAcrossTenants(t *testing.T) {
	t.Parallel()

	prod := &silenceMutateClient{hit: true, silence: activeSilence()}
	staging := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"}, config.Backend{Name: "staging"})
	build := func(be config.Backend) (backend.Client, error) {
		if be.Name == "prod" {
			return prod, nil
		}
		return staging, nil
	}

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Ends: "4h"}, "")
	require.NoError(t, err)
	require.Equal(t, "prod\tsil-1\nstaging\tsil-1\n", out.String())
	require.Equal(t, "verify with: a10r silences get sil-1\n", errOut.String(),
		"update emits a single get verify hint even when mirrored across tenants")
	require.NotNil(t, prod.updated)
	require.NotNil(t, staging.updated)
}

func TestSilenceUpdate_MergeValidationAborts(t *testing.T) {
	t.Parallel()

	// New start lands after the existing end, and --ends is not given, so
	// the merged ends <= starts: the whole command must abort with nothing
	// written.
	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Starts: testNow.Add(5 * time.Hour).Format(time.RFC3339)}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ends must be after starts")
	require.Nil(t, client.updated, "an invalid merge writes nothing")
}

func TestSilenceUpdate_ReplaceMatchers(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Matchers: []string{`team="db"`, `env="prod"`}}, "")
	require.NoError(t, err)
	require.NotNil(t, client.updated)
	require.Len(t, client.updated.Matchers, 2, "matcher flags replace the whole set")
	require.Equal(t, "team", client.updated.Matchers[0].Name)
}

func TestSilenceUpdate_NoFlagsErrors(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{}, "")
	require.Error(t, err)
	require.Nil(t, client.updated)
}

func TestSilenceUpdate_NotFoundExits5(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: false}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "ghost",
		silenceUpdateOptions{Comment: "x"}, "")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
}

func TestSilenceUpdate_ExpiredPointsToRecreate(t *testing.T) {
	t.Parallel()

	expired := activeSilence()
	expired.State = backend.SilenceStateExpired
	client := &silenceMutateClient{hit: true, silence: expired}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Ends: "4h"}, "")
	require.Error(t, err)
	require.Contains(t, errOut.String(), "recreate")
	require.Nil(t, client.updated, "an expired silence is not updated in place")
}

func TestSilenceUpdate_ReadOnlyTargetFailsClosed(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod", ReadOnly: true})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Ends: "4h"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "prod")
	require.Nil(t, client.updated)
}

func TestSilenceUpdate_GlobalReadOnlyFailsFast(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, true, build, testNow, "sil-1",
		silenceUpdateOptions{Ends: "4h"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-only")
	require.Nil(t, client.updated)
}

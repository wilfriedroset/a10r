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

// silenceRecreateClient fakes the get-by-id + create surface: GetSilence
// returns the source silence, CreateSilence records the new spec.
type silenceRecreateClient struct {
	backendtest.ClientStub
	source   backend.Silence
	hit      bool
	createID string
	created  *backend.SilenceSpec
}

func (c *silenceRecreateClient) GetSilence(context.Context, string) (backend.Silence, error) {
	if !c.hit {
		return backend.Silence{}, backend.ErrNotFound
	}
	return c.source, nil
}

func (c *silenceRecreateClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	c.created = &spec
	return c.createID, nil
}

func sourceSilence(state backend.SilenceState) backend.Silence {
	return backend.Silence{
		ID:        "sil-old",
		State:     state,
		CreatedBy: "bob",
		Comment:   "disk pressure",
		StartsAt:  testNow.Add(-48 * time.Hour),
		EndsAt:    testNow.Add(-24 * time.Hour),
		Matchers:  []backend.Matcher{{Name: "alertname", Value: "DiskFull", IsEqual: true}},
	}
}

func TestSilenceRecreate_CopiesMatchersAndCommentResetsRest(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: true, source: sourceSilence(backend.SilenceStateExpired), createID: "sil-new"}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h"}, "alice")
	require.NoError(t, err)
	require.Equal(t, "prod\tsil-new\n", out.String())
	require.NotNil(t, client.created)
	require.Equal(t, "disk pressure", client.created.Comment, "comment copied")
	require.Len(t, client.created.Matchers, 1)
	require.Equal(t, "DiskFull", client.created.Matchers[0].Value, "matchers copied")
	require.Equal(t, "alice", client.created.CreatedBy, "creator reset to the acting user, not the source's")
	require.True(t, client.created.StartsAt.Equal(testNow), "start resets to now")
	require.True(t, client.created.EndsAt.Equal(testNow.Add(2*time.Hour)), "window restated from now")
}

func TestSilenceRecreate_EndsRequired(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: true, source: sourceSilence(backend.SilenceStateExpired)}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--ends")
	require.Nil(t, client.created, "no window assumed; nothing created without --ends")
}

func TestSilenceRecreate_CommentOverride(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: true, source: sourceSilence(backend.SilenceStateActive), createID: "sil-new"}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h", Comment: "follow-up"}, "alice")
	require.NoError(t, err)
	require.Equal(t, "follow-up", client.created.Comment)
}

func TestSilenceRecreate_MirroredAcrossTenants(t *testing.T) {
	t.Parallel()

	src := sourceSilence(backend.SilenceStateExpired)
	prod := &silenceRecreateClient{hit: true, source: src, createID: "p-new"}
	staging := &silenceRecreateClient{hit: true, source: src, createID: "s-new"}
	cfg := cfgWith(config.Backend{Name: "prod"}, config.Backend{Name: "staging"})
	build := func(be config.Backend) (backend.Client, error) {
		if be.Name == "prod" {
			return prod, nil
		}
		return staging, nil
	}

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h"}, "alice")
	require.NoError(t, err)
	require.Equal(t, "prod\tp-new\nstaging\ts-new\n", out.String())
	require.NotNil(t, prod.created)
	require.NotNil(t, staging.created)
}

func TestSilenceRecreate_EmptyCommentNoOverrideAborts(t *testing.T) {
	t.Parallel()

	src := sourceSilence(backend.SilenceStateExpired)
	src.Comment = ""
	client := &silenceRecreateClient{hit: true, source: src}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "comment")
	require.Nil(t, client.created)
}

func TestSilenceRecreate_NotFoundExits5(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: false}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "ghost",
		silenceRecreateOptions{Ends: "2h"}, "alice")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
}

func TestSilenceRecreate_ReadOnlyTargetFailsClosed(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: true, source: sourceSilence(backend.SilenceStateActive)}
	cfg := cfgWith(config.Backend{Name: "prod", ReadOnly: true})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "prod")
	require.Nil(t, client.created)
}

func TestSilenceRecreate_GlobalReadOnlyFailsClosed(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: true, source: sourceSilence(backend.SilenceStateActive)}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, true, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h"}, "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-only")
	require.Nil(t, client.created)
}

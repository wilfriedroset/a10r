// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/output"
)

var errSkipTest = errors.New("already expired")

func TestSilenceCreate_DryRunLinesNoWrite(t *testing.T) {
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
			DryRun:   true,
		}, "alice", "")
	require.NoError(t, err)
	require.Nil(t, client.created, "dry-run must not call CreateSilence")
	require.Contains(t, out.String(), "would create")
	require.Contains(t, out.String(), "prod")
	require.Contains(t, out.String(), `severity="critical"`)
	require.Contains(t, out.String(), "from "+testNow.UTC().Format(time.RFC3339),
		"lines mode shows the start, not just the end")
	require.Contains(t, out.String(), "until "+testNow.Add(2*time.Hour).UTC().Format(time.RFC3339))
	require.Empty(t, errOut.String(), "no hint and no read-only note in a clean writable dry-run")
}

func TestSilenceCreate_DryRunJSON(t *testing.T) {
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
			DryRun:   true,
		}, "alice", output.FormatJSON)
	require.NoError(t, err)
	require.Nil(t, client.created)

	var got []plannedWrite
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "create", got[0].Action)
	require.Equal(t, "prod", got[0].Tenant)
	require.Empty(t, got[0].ID, "create mints the id at apply, so dry-run has none")
	require.Equal(t, []string{`severity="critical"`}, got[0].Matchers)
	require.Equal(t, "maint", got[0].Comment)
	require.Equal(t, "alice", got[0].CreatedBy)
	require.NotEmpty(t, got[0].StartsAt)
	require.NotEmpty(t, got[0].EndsAt)
}

func TestRunDryRun_ExpireOmitsSpecFields(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	targets := []writeTarget{{tenant: "prod", id: "sil-1"}} // expire carries an id, no spec

	var out, errOut bytes.Buffer
	require.NoError(t, runDryRun(&out, &errOut, cfg, output.FormatJSON, "expire", targets, false))

	var got []plannedWrite
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "sil-1", got[0].ID)
	require.Empty(t, got[0].Matchers, "expire has no spec, so the plan omits matchers")
	require.Empty(t, got[0].EndsAt, "expire has no spec, so the plan omits the window")
}

func TestSilenceCreate_DryRunUnderGlobalReadOnlyStillPlans(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{createID: "new-1"}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, true, build, testNow, true,
		silenceCreateOptions{
			Matchers: []string{`a="b"`},
			Ends:     "2h",
			Comment:  "m",
			DryRun:   true,
		}, "alice", "")
	require.NoError(t, err, "dry-run plans even under read-only; it does not abort")
	require.Nil(t, client.created)
	require.Contains(t, out.String(), "would create")
	require.Contains(t, errOut.String(), "read-only", "lines mode notes read-only on stderr")
}

func TestSilenceCreate_DryRunReadOnlyBackendStructuredField(t *testing.T) {
	t.Parallel()

	client := &silenceWriteClient{createID: "new-1"}
	cfg := cfgWith(config.Backend{Name: "prod", ReadOnly: true})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceCreate(context.Background(), &out, &errOut, cfg, false, build, testNow, true,
		silenceCreateOptions{
			Matchers: []string{`a="b"`},
			Ends:     "2h",
			Comment:  "m",
			DryRun:   true,
		}, "alice", output.FormatJSON)
	require.NoError(t, err)
	require.Nil(t, client.created)

	var got []plannedWrite
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.True(t, got[0].ReadOnly, "a read-only backend rides the read_only field in structured mode")
	require.Empty(t, errOut.String(), "no stderr note in structured mode")
}

func TestSilenceExpire_DryRunActiveNoWrite(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{activeS("sil-1")}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", true)
	require.NoError(t, err)
	require.Empty(t, client.expired, "dry-run must not call ExpireSilence")
	require.Contains(t, out.String(), "would expire")
	require.Contains(t, out.String(), "sil-1")
}

func TestSilenceExpire_DryRunAlreadyExpiredSkipExitsNonZero(t *testing.T) {
	t.Parallel()

	client := &silenceExpireClient{silences: []backend.Silence{{ID: "sil-1", State: backend.SilenceStateExpired}}}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceExpire(context.Background(), &out, &errOut, cfg, false, build, []string{"sil-1"}, "", true)
	require.Error(t, err, "a skipped target exits non-zero, mirroring the real run")
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Empty(t, client.expired)
	require.Contains(t, out.String(), "already expired")
}

func TestSilenceUpdate_DryRunMergedSpecNoWrite(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Comment: "patched", DryRun: true}, "")
	require.NoError(t, err)
	require.Nil(t, client.updated, "dry-run must not call UpdateSilence")
	require.Contains(t, out.String(), "would update")
	require.Contains(t, out.String(), "sil-1")
}

func TestSilenceUpdate_DryRunMergedSpecJSONCarriesPatch(t *testing.T) {
	t.Parallel()

	client := &silenceMutateClient{hit: true, silence: activeSilence()}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceUpdate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-1",
		silenceUpdateOptions{Comment: "patched", DryRun: true}, output.FormatJSON)
	require.NoError(t, err)
	require.Nil(t, client.updated)

	var got []plannedWrite
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "update", got[0].Action)
	require.Equal(t, "sil-1", got[0].ID)
	require.Equal(t, "patched", got[0].Comment, "the merged comment override is shown")
}

func TestSilenceRecreate_DryRunNewSpecNoWrite(t *testing.T) {
	t.Parallel()

	client := &silenceRecreateClient{hit: true, source: sourceSilence(backend.SilenceStateExpired), createID: "sil-new"}
	cfg := cfgWith(config.Backend{Name: "prod"})
	build := func(config.Backend) (backend.Client, error) { return client, nil }

	var out, errOut bytes.Buffer
	err := silenceRecreate(context.Background(), &out, &errOut, cfg, false, build, testNow, "sil-old",
		silenceRecreateOptions{Ends: "2h", DryRun: true}, "alice", "")
	require.NoError(t, err)
	require.Nil(t, client.created, "dry-run must not call CreateSilence")
	require.Contains(t, out.String(), "would recreate")
}

func TestRunDryRun_ExitCodeCleanIsNil(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	targets := []writeTarget{{tenant: "prod", id: "sil-1"}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", "expire", targets, false)
	require.NoError(t, err)
}

func TestRunDryRun_ExitCodeSkipIsNonZero(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	targets := []writeTarget{{tenant: "prod", id: "sil-1", skip: errSkipTest}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, "", "expire", targets, false)
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
}

func TestRunDryRun_SpecRendersMatchersAndTimes(t *testing.T) {
	t.Parallel()

	cfg := cfgWith(config.Backend{Name: "prod"})
	targets := []writeTarget{{tenant: "prod", spec: backend.SilenceSpec{
		Matchers:  []backend.Matcher{{Name: "severity", Value: "critical", IsEqual: true}},
		StartsAt:  testNow,
		EndsAt:    testNow.Add(2 * time.Hour),
		Comment:   "m",
		CreatedBy: "alice",
	}}}

	var out, errOut bytes.Buffer
	err := runDryRun(&out, &errOut, cfg, output.FormatYAML, "create", targets, false)
	require.NoError(t, err)
	require.Contains(t, out.String(), "severity")
	require.Contains(t, out.String(), testNow.Add(2*time.Hour).UTC().Format(time.RFC3339))
}

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
)

// silenceGetClient fakes GetSilence: a hit returns the silence, a miss
// returns the typed ErrNotFound, and err (when set) stands in for a
// transport failure.
type silenceGetClient struct {
	backendtest.ClientStub
	silence backend.Silence
	hit     bool
	err     error
}

func (c silenceGetClient) GetSilence(context.Context, string) (backend.Silence, error) {
	if c.err != nil {
		return backend.Silence{}, c.err
	}
	if !c.hit {
		return backend.Silence{}, backend.ErrNotFound
	}
	return c.silence, nil
}

func TestSilenceGet_FoundRendersDetail(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod": silenceGetClient{hit: true, silence: backend.Silence{
			ID:        "sil-1",
			State:     backend.SilenceStateActive,
			CreatedBy: "alice",
			Comment:   "maintenance",
			Matchers:  []backend.Matcher{{Name: "severity", Value: "critical", IsEqual: true}},
		}},
		"staging": silenceGetClient{hit: false},
	})

	var out, errOut bytes.Buffer
	err := silenceGet(context.Background(), &out, &errOut, cfg, build, "sil-1", "json")
	require.NoError(t, err)

	var got silenceRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, "prod", got.Tenant)
	require.Equal(t, "sil-1", got.ID)
	require.Equal(t, "alice", got.CreatedBy)
	require.Empty(t, errOut.String(), "a miss on another backend is not a failure")
}

func TestSilenceGet_AbsentEverywhereExits5(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod":    silenceGetClient{hit: false},
		"staging": silenceGetClient{hit: false},
	})

	var out, errOut bytes.Buffer
	err := silenceGet(context.Background(), &out, &errOut, cfg, build, "ghost", "json")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitNotFound, ex.Code)
	require.Empty(t, errOut.String(), "a clean not-found is not a backend failure")
}

func TestSilenceGet_PartialFailureStillRendersMatch(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod": silenceGetClient{err: errors.New("dial tcp: refused")},
		"staging": silenceGetClient{hit: true, silence: backend.Silence{
			ID: "sil-1", State: backend.SilenceStateActive, CreatedBy: "bob", Comment: "x",
		}},
	})

	var out, errOut bytes.Buffer
	err := silenceGet(context.Background(), &out, &errOut, cfg, build, "sil-1", "json")
	require.NoError(t, err)

	var got silenceRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, "staging", got.Tenant)
	require.Contains(t, errOut.String(), `backend "prod"`)
}

func TestSilenceGet_MirroredMatchRendersSequence(t *testing.T) {
	t.Parallel()

	mirrored := backend.Silence{ID: "sil-1", State: backend.SilenceStateActive, CreatedBy: "a", Comment: "c"}
	cfg, build := buildFactory(map[string]backend.Client{
		"prod":    silenceGetClient{hit: true, silence: mirrored},
		"staging": silenceGetClient{hit: true, silence: mirrored},
	})

	var out, errOut bytes.Buffer
	err := silenceGet(context.Background(), &out, &errOut, cfg, build, "sil-1", "json")
	require.NoError(t, err)

	var got []silenceRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "prod", got[0].Tenant)
	require.Equal(t, "staging", got[1].Tenant)
}

func TestSilenceGet_AllBackendsFailExitsUnreachable(t *testing.T) {
	t.Parallel()

	cfg, build := buildFactory(map[string]backend.Client{
		"prod":    silenceGetClient{err: errors.New("dial tcp: refused")},
		"staging": silenceGetClient{err: errors.New("dial tcp: refused")},
	})

	var out, errOut bytes.Buffer
	err := silenceGet(context.Background(), &out, &errOut, cfg, build, "sil-1", "json")
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex)
	require.Equal(t, ExitUnreachable, ex.Code)
}

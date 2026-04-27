// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func sampleSilence() backend.Silence {
	return backend.Silence{
		ID:        "sil-7",
		Comment:   "ack while patching",
		CreatedBy: "alice",
		StartsAt:  time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC),
		EndsAt:    time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC),
		Matchers: []backend.Matcher{
			{Name: "alertname", Value: "HighCPU", IsEqual: true},
			{Name: "severity", Value: "warning|critical", IsRegex: true, IsEqual: true},
		},
	}
}

func TestYAML_RoundTrip(t *testing.T) {
	t.Parallel()
	in := sampleSilence()
	body, err := silenceToYAML(in)
	require.NoError(t, err)
	id, spec, err := silenceFromYAML(body)
	require.NoError(t, err)
	require.Equal(t, in.ID, id)
	require.Equal(t, in.Comment, spec.Comment)
	require.Equal(t, in.CreatedBy, spec.CreatedBy)
	require.True(t, in.StartsAt.Equal(spec.StartsAt))
	require.True(t, in.EndsAt.Equal(spec.EndsAt))
	require.Equal(t, in.Matchers, spec.Matchers)
}

func TestYAML_FromYAML_RejectsEmpty(t *testing.T) {
	t.Parallel()
	_, _, err := silenceFromYAML([]byte("   \n  "))
	require.ErrorContains(t, err, "empty")
}

func TestYAML_FromYAML_RequiresID(t *testing.T) {
	t.Parallel()
	body, err := silenceToYAML(sampleSilence())
	require.NoError(t, err)
	// Strip the id line.
	stripped := strings.ReplaceAll(string(body), "id: sil-7", "id: \"\"")
	_, _, err = silenceFromYAML([]byte(stripped))
	require.ErrorContains(t, err, "id is required")
}

func TestYAML_FromYAML_RequiresAtLeastOneMatcher(t *testing.T) {
	t.Parallel()
	in := sampleSilence()
	in.Matchers = nil
	body, err := silenceToYAML(in)
	require.NoError(t, err)
	_, _, err = silenceFromYAML(body)
	require.ErrorContains(t, err, "matcher")
}

func TestYAML_FromYAML_RejectsEndsBeforeStarts(t *testing.T) {
	t.Parallel()
	in := sampleSilence()
	in.EndsAt = in.StartsAt.Add(-time.Hour)
	body, err := silenceToYAML(in)
	require.NoError(t, err)
	_, _, err = silenceFromYAML(body)
	require.ErrorContains(t, err, "endsAt must be after startsAt")
}

func TestYAML_FromYAML_RequiresCreator(t *testing.T) {
	t.Parallel()
	in := sampleSilence()
	in.CreatedBy = ""
	body, err := silenceToYAML(in)
	require.NoError(t, err)
	_, _, err = silenceFromYAML(body)
	require.ErrorContains(t, err, "createdBy is required")
}

func TestYAML_FromYAML_RequiresComment(t *testing.T) {
	t.Parallel()
	in := sampleSilence()
	in.Comment = ""
	body, err := silenceToYAML(in)
	require.NoError(t, err)
	_, _, err = silenceFromYAML(body)
	require.ErrorContains(t, err, "comment is required")
}

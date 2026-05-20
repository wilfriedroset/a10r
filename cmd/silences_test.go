// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func TestValidateSilenceState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty is no-op", in: "", want: ""},
		{name: "lowercase active", in: "active", want: "active"},
		{name: "mixed case folds", in: "PENDING", want: "pending"},
		{name: "expired", in: "expired", want: "expired"},
		{name: "trim whitespace", in: "  active  ", want: "active"},
		// The unknown-state case covers the entire default-error
		// branch — adding more invalid inputs ("typo", "armed") would
		// re-test the same `else { return error }` arm of the
		// validate-state switch with no extra catching power.
		{name: "unknown fails closed", in: "armed", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateSilenceState(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestToSilenceRow_PreservesShape(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	got := toSilenceRow("prod", backend.Silence{
		ID:        "abc",
		State:     backend.SilenceStateActive,
		CreatedBy: "alice",
		Comment:   "deploy window",
		StartsAt:  now,
		EndsAt:    now.Add(time.Hour),
		Matchers: []backend.Matcher{
			{Name: "severity", Value: "critical", IsEqual: true},
		},
	})
	require.Equal(t, "prod", got.Tenant)
	require.Equal(t, "abc", got.ID)
	require.Equal(t, backend.SilenceStateActive, got.State)
	require.Equal(t, "alice", got.CreatedBy)
	require.Equal(t, "deploy window", got.Comment)
	require.Equal(t, now, got.StartsAt)
	require.Len(t, got.Matchers, 1)
}

func TestFilterSilenceRows_ByState(t *testing.T) {
	t.Parallel()

	rows := []silenceRow{
		{ID: "1", State: backend.SilenceStateActive},
		{ID: "2", State: backend.SilenceStateExpired},
		{ID: "3", State: backend.SilenceStatePending},
	}
	got := filterSilenceRows(rows, "active", nil)
	require.Len(t, got, 1)
	require.Equal(t, "1", got[0].ID)
}

func TestFilterSilenceRows_ByMatcherStrictTuple(t *testing.T) {
	t.Parallel()

	rows := []silenceRow{
		{
			ID: "regex",
			Matchers: []matcherRow{
				{Name: "severity", Value: "critical", IsRegex: true, IsEqual: true},
			},
		},
		{
			ID: "literal",
			Matchers: []matcherRow{
				{Name: "severity", Value: "critical", IsEqual: true},
			},
		},
	}
	wanted := backend.Matcher{Name: "severity", Value: "critical", IsEqual: true}
	got := filterSilenceRows(rows, "", &wanted)
	require.Len(t, got, 1, "regex variant must NOT match a literal predicate even when value strings collide")
	require.Equal(t, "literal", got[0].ID)
}

func TestFilterSilenceRows_NoFiltersIsIdentity(t *testing.T) {
	t.Parallel()

	rows := []silenceRow{
		{ID: "a", State: backend.SilenceStateActive},
		{ID: "b", State: backend.SilenceStatePending},
	}
	got := filterSilenceRows(rows, "", nil)
	require.Len(t, got, 2)
}

func TestSortSilenceRows_TenantThenStateThenID(t *testing.T) {
	t.Parallel()

	rows := []silenceRow{
		{Tenant: "staging", ID: "1", State: backend.SilenceStateActive},
		{Tenant: "prod", ID: "2", State: backend.SilenceStateExpired},
		{Tenant: "prod", ID: "3", State: backend.SilenceStateActive},
		{Tenant: "prod", ID: "1", State: backend.SilenceStateActive},
	}
	sortSilenceRows(rows)
	require.Equal(t, "prod", rows[0].Tenant)
	require.Equal(t, backend.SilenceStateActive, rows[0].State)
	require.Equal(t, "1", rows[0].ID)
	require.Equal(t, "prod", rows[1].Tenant)
	require.Equal(t, "3", rows[1].ID)
	require.Equal(t, "prod", rows[2].Tenant)
	require.Equal(t, backend.SilenceStateExpired, rows[2].State)
	require.Equal(t, "staging", rows[3].Tenant)
}

func TestRenderSilenceRows_TableHeaderAndCells(t *testing.T) {
	t.Parallel()

	rows := []silenceRow{
		{
			Tenant: "prod", ID: "abc",
			State:    backend.SilenceStateActive,
			EndsAt:   time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
			Matchers: []matcherRow{{Name: "severity", Value: "critical", IsEqual: true}},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, renderSilenceTable(&buf, rows))
	out := buf.String()
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "ID")
	require.Contains(t, out, "MATCHERS")
	require.Contains(t, out, "abc")
	require.Contains(t, out, "active")
	require.Contains(t, out, `severity="critical"`)
}

func TestRenderSilenceRows_JSONIncludesMatchers(t *testing.T) {
	t.Parallel()

	rows := []silenceRow{
		{
			Tenant: "prod", ID: "abc",
			State:    backend.SilenceStateActive,
			Matchers: []matcherRow{{Name: "severity", Value: "critical", IsEqual: true}},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, renderSilenceJSON(&buf, rows))
	out := buf.String()
	require.Contains(t, out, `"tenant": "prod"`)
	require.Contains(t, out, `"state": "active"`)
	require.Contains(t, out, `"name": "severity"`,
		"matchers slice round-trips through JSON output with lowercase keys")
	require.Contains(t, out, `"isEqual": true`,
		"matcher operator flags use the documented lower-camel keys")
}

func TestSummariseMatchers_AllOperators(t *testing.T) {
	t.Parallel()

	got := summariseMatchers([]matcherRow{
		{Name: "a", Value: "1", IsEqual: true},
		{Name: "b", Value: "2"},
		{Name: "c", Value: "3", IsRegex: true, IsEqual: true},
		{Name: "d", Value: "4", IsRegex: true},
	})
	require.Equal(t, `a="1",b!="2",c=~"3",d!~"4"`, got)
}

// TestRunSilencesList_FailWhenAllBackendsDown exercises the "every
// backend in the active scope failed" branch of runSilencesList:
// with --fail set and a config that only references unreachable
// backends, the helper must surface ExitUnreachable. Mirrors the
// expectation the brief calls out: `--fail` against a down backend
// exits non-zero.
func TestRunSilencesList_FailWhenAllBackendsDown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a10r.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
backends:
  - name: down
    url: http://127.0.0.1:1
`), 0o600))

	flags := &GlobalFlags{ConfigPath: cfgPath}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runSilencesList(ctx, &buf, flags, silencesListOptions{
		Output:    "json",
		FailOnAny: true,
	})
	require.Error(t, err)
	var ex *ExitError
	require.ErrorAs(t, err, &ex, "must wrap ExitError")
	require.Equal(t, ExitUnreachable, ex.Code)
}

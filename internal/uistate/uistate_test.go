// SPDX-License-Identifier: Apache-2.0

package uistate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/uistate"
)

// statePath is the on-disk file name the whole feature keys off.
// Hard-coded here (not imported) so a rename shows up as a failing
// test rather than a silently migrated file.
const statePath = "ui-state.yaml"

func TestPruneScope(t *testing.T) {
	t.Parallel()
	known := []string{"prod", "staging", "dev"}
	cases := []struct {
		name        string
		scope       string
		known       []string
		want        string
		wantDropped []string
	}{
		{name: "all stays all", scope: "all", known: known, want: "all"},
		{name: "empty reads as all", scope: "", known: known, want: "all"},
		{name: "known name survives", scope: "prod", known: known, want: "prod"},
		{name: "unknown name drops to all", scope: "gone", known: known, want: "all", wantDropped: []string{"gone"}},
		{name: "comma list keeps order", scope: "dev,prod", known: known, want: "dev,prod"},
		{name: "partial list keeps survivors", scope: "prod,gone,dev", known: known, want: "prod,dev", wantDropped: []string{"gone"}},
		{name: "nothing survives", scope: "gone,also-gone", known: known, want: "all", wantDropped: []string{"gone", "also-gone"}},
		{name: "no backends configured", scope: "prod", known: nil, want: "all", wantDropped: []string{"prod"}},
		{name: "hand-edited spaces drop nothing", scope: "prod, staging", known: known, want: "prod,staging"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, dropped := uistate.PruneScope(tc.scope, tc.known)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.wantDropped, dropped)
		})
	}
}

func TestStore_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s := uistate.Open(dir)
	s.SetScope("prod")
	s.SetSort("alerts", "severity:desc")
	s.SetSort("silences", "ends:asc")
	require.NoError(t, s.Close())

	reopened := uistate.Open(dir)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.Equal(t, "prod", reopened.Scope())
	require.Equal(t, "severity:desc", reopened.Sort("alerts"))
	require.Equal(t, "ends:asc", reopened.Sort("silences"))
	require.Empty(t, reopened.Sort("receivers"), "an unremembered page reads empty")
}

func TestStore_ForgetRemovesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, statePath)

	s := uistate.Open(dir)
	s.SetScope("prod")
	s.SetSort("alerts", "severity:desc")
	require.NoError(t, s.Close())
	require.FileExists(t, path)

	s2 := uistate.Open(dir)
	s2.SetScope("all")
	s2.SetSort("alerts", "")
	require.NoError(t, s2.Close())
	require.NoFileExists(t, path, "a state with nothing to remember removes the file")
}

func TestStore_LastWriteWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	s := uistate.Open(dir)
	for _, v := range []string{"a:asc", "b:desc", "c:asc", "severity:desc"} {
		s.SetSort("alerts", v)
	}
	require.NoError(t, s.Close())

	body, err := os.ReadFile(filepath.Join(dir, statePath))
	require.NoError(t, err)
	require.Contains(t, string(body), "severity:desc",
		"the newest value is what lands, however many changes raced the writer")

	reopened := uistate.Open(dir)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.Equal(t, "severity:desc", reopened.Sort("alerts"))
}

// A malformed file must survive a run that changes the sort: the
// store goes read-only rather than flush an empty state over the
// user's typo.
func TestStore_MalformedFileSurvivesAMutation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, statePath)
	original := "scope: prod\nnope: 1\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	s := uistate.Open(dir)
	s.SetScope("staging")
	s.SetSort("alerts", "severity:desc")
	require.NoError(t, s.Close())

	body, err := os.ReadFile(path)
	require.NoError(t, err, "the malformed file must still be there")
	require.Equal(t, original, string(body), "and must be byte-identical")
}

func TestStore_MalformedFileYieldsEmptyStateAndSurvives(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{name: "not yaml", body: "scope: [unterminated\n"},
		{name: "unknown key rejected by strict decoding", body: "scope: prod\nnope: 1\n"},
		{name: "wrong type", body: "scope:\n  nested: true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, statePath)
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))

			s := uistate.Open(dir)
			require.Empty(t, s.Scope())
			require.Empty(t, s.Sort("alerts"))
			require.NoError(t, s.Close())
			require.FileExists(t, path, "a malformed file is never rewritten or deleted")

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tc.body, string(got))
		})
	}
}

func TestStore_MissingFileIsFirstRun(t *testing.T) {
	t.Parallel()
	s := uistate.Open(t.TempDir())
	require.Empty(t, s.Scope())
	require.Empty(t, s.Sort("alerts"))
	require.NoError(t, s.Close())
}

func TestStore_EmptyDirNeverTouchesDisk(t *testing.T) {
	t.Parallel()
	cwd, err := os.Getwd()
	require.NoError(t, err)

	s := uistate.Open("")
	s.SetScope("prod")
	s.SetSort("alerts", "severity:desc")
	require.Empty(t, s.Scope(), "a disabled store reads zero values")
	require.Empty(t, s.Sort("alerts"))
	require.NoError(t, s.Close())

	require.NoFileExists(t, filepath.Join(cwd, statePath))
	require.NoFileExists(t, statePath)
}

// TestStore_UnwritableDirFailsSoft covers the "state dir is not
// writable" degrade path: writes are dropped, reads still answer,
// and no error ever reaches the caller.
func TestStore_UnwritableDirFailsSoft(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "locked")
	require.NoError(t, os.Mkdir(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	s := uistate.Open(filepath.Join(dir, "sub"))
	s.SetScope("prod")
	require.Equal(t, "prod", s.Scope(), "reads are unaffected by a failed write")
	require.NoError(t, s.Close())
	require.NoFileExists(t, filepath.Join(dir, "sub", statePath))
}

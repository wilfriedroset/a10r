// SPDX-License-Identifier: Apache-2.0

package footer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// envMap returns a stable env-lookup func over the supplied map so
// tests can drive HistoryDir branches without setenv contamination.
func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

func TestHistoryDir_PrefersXDGStateHome(t *testing.T) {
	t.Parallel()

	stateRoot := filepath.Join(string(filepath.Separator), "state")
	homeRoot := filepath.Join(string(filepath.Separator), "home", "u")

	got, err := HistoryDir(
		envMap(map[string]string{"XDG_STATE_HOME": stateRoot}),
		func() (string, error) { return homeRoot, nil },
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(stateRoot, "a10r"), got)
}

func TestHistoryDir_FallsBackToHome(t *testing.T) {
	t.Parallel()

	homeRoot := filepath.Join(string(filepath.Separator), "home", "u")

	got, err := HistoryDir(
		envMap(nil),
		func() (string, error) { return homeRoot, nil },
	)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(homeRoot, ".local", "state", "a10r"), got)
}

func TestHistoryDir_HomeErrorPropagates(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("no home")
	_, err := HistoryDir(
		envMap(nil),
		func() (string, error) { return "", sentinel },
	)
	require.ErrorIs(t, err, sentinel)
}

func TestHistory_NewHistoryEmptyDirSkipsPersistence(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	require.NotNil(t, h)
	require.Empty(t, h.Path(), "empty dir disables persistence")
	h.Append("x")
	require.Equal(t, 1, h.Len(),
		"in-memory ring still works without a backing file")
}

func TestHistory_AppendPersistsAndRoundTrips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	h := NewHistory(dir, HistoryFilter)
	h.Append("alerts")
	h.Append("silences")

	// File mode must be 0o600 — no group/world access.
	info, err := os.Stat(filepath.Join(dir, string(HistoryFilter)))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(HistoryFileMode), info.Mode().Perm(),
		"history file must be 0o600 — leaks recent matcher queries on shared hosts otherwise")

	// Round-trip: a fresh History over the same dir sees the entries.
	h2 := NewHistory(dir, HistoryFilter)
	require.Equal(t, 2, h2.Len())
}

func TestHistory_AppendDropsEmpty(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("")
	require.Zero(t, h.Len(), "empty input must not be appended")
}

func TestHistory_AdjacentDedup(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("alerts")
	h.Append("alerts")
	require.Equal(t, 1, h.Len(),
		"a submission equal to the prior entry must dedup")
}

func TestHistory_NonAdjacentDuplicatesKept(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("alerts")
	h.Append("silences")
	h.Append("alerts")
	require.Equal(t, 3, h.Len(),
		"non-adjacent duplicates are intentional — dedup'ing them would hide a recall")
}

func TestHistory_CapEvictsOldest(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	for i := range historyMaxEntries + 10 {
		// Distinct values so adjacent dedup doesn't interfere.
		h.Append("entry-" + itoa(i))
	}
	require.Equal(t, historyMaxEntries, h.Len(),
		"ring must hold at most historyMaxEntries entries")

	// First 10 entries should be evicted — Prev() must walk the
	// surviving tail. Newest is "entry-<max+9>".
	v, ok := h.Prev("")
	require.True(t, ok)
	require.Equal(t, "entry-"+itoa(historyMaxEntries+9), v,
		"newest entry survives and is the first to surface on Prev")

	// Walk to the oldest — should be "entry-10" (entries 0..9 evicted).
	for range historyMaxEntries - 1 {
		_, ok := h.Prev("")
		require.True(t, ok)
	}
	// Already at oldest now.
	_, ok = h.Prev("")
	require.False(t, ok, "Prev past oldest must report no-op after eviction")
}

func TestHistory_PrevWalksOldestStops(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("a")
	h.Append("b")
	h.Append("c")

	v, ok := h.Prev("draft")
	require.True(t, ok)
	require.Equal(t, "c", v, "first Prev surfaces the newest entry")

	v, ok = h.Prev("draft")
	require.True(t, ok)
	require.Equal(t, "b", v)

	v, ok = h.Prev("draft")
	require.True(t, ok)
	require.Equal(t, "a", v, "Prev keeps walking until the oldest")

	_, ok = h.Prev("draft")
	require.False(t, ok, "Prev past the oldest must report no-op, not wrap")
}

func TestHistory_NextRestoresDraftAtPresent(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("a")
	h.Append("b")

	_, _ = h.Prev("typed-but-not-submitted")
	_, _ = h.Prev("typed-but-not-submitted")

	v, ok := h.Next()
	require.True(t, ok)
	require.Equal(t, "b", v, "Next walks back toward the newest")

	v, ok = h.Next()
	require.True(t, ok)
	require.Equal(t, "typed-but-not-submitted", v,
		"crossing the newest restores the pre-cycle draft")

	require.False(t, h.Cycling(),
		"after the draft restore, the cycle session must end")
}

func TestHistory_NextWithoutPrevIsNoOp(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("a")

	_, ok := h.Next()
	require.False(t, ok,
		"Next without a prior Prev must report no-op (cursor already at present)")
}

func TestHistory_AppendResetsCycleState(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("a")
	h.Append("b")

	_, _ = h.Prev("")
	require.True(t, h.Cycling())
	h.Append("c")
	require.False(t, h.Cycling(),
		"submission must end the cycle session so a re-open starts fresh")
}

func TestHistory_MalformedFileDegradesGracefully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, string(HistoryFilter))
	// Pre-seed with a file containing blank lines, surrounding
	// whitespace, and a trailing newline — the realistic shape of
	// a hand-edited file. Trimming + blank-skip should give us two
	// entries.
	require.NoError(t, os.WriteFile(path,
		[]byte("\n  alerts  \n\nsilences\n\n"),
		0o600,
	))
	h := NewHistory(dir, HistoryFilter)
	require.Equal(t, 2, h.Len(),
		"malformed file (blanks, whitespace) must degrade to a clean ring")
	v, ok := h.Prev("")
	require.True(t, ok)
	require.Equal(t, "silences", v)
}

func TestHistory_UnreadableFileDegradesToEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create a directory at the path so os.Open errors with EISDIR.
	// Cross-platform stand-in for a "this file can't be read"
	// failure mode that doesn't depend on chmod 000 (which behaves
	// differently across OSes and CI environments).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, string(HistoryFilter)), 0o755))

	h := NewHistory(dir, HistoryFilter)
	require.Zero(t, h.Len(),
		"an unreadable backing file must surface as an empty ring, not a panic")
}

func TestHistory_ResetClearsCursorAndDraft(t *testing.T) {
	t.Parallel()

	h := NewHistory("", HistoryFilter)
	h.Append("a")
	_, _ = h.Prev("draft")
	require.True(t, h.Cycling())

	h.Reset()
	require.False(t, h.Cycling())

	// After Reset, Next is a no-op (no cycle in progress).
	_, ok := h.Next()
	require.False(t, ok)
}

func TestHistory_NilSafeMethods(t *testing.T) {
	t.Parallel()

	// Nil History is the "no persistence wired" sentinel — every
	// method is a quiet no-op so the prompt can stay un-conditional.
	var h *History
	require.Zero(t, h.Len())
	require.Empty(t, h.Path())
	h.Append("ignored")
	_, ok := h.Prev("")
	require.False(t, ok)
	_, ok = h.Next()
	require.False(t, ok)
	require.False(t, h.Cycling())
	h.Reset() // must not panic
}

func TestHistory_FileExcludesEvictedEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	h := NewHistory(dir, HistoryFilter)
	for i := range historyMaxEntries + 5 {
		h.Append("entry-" + itoa(i))
	}
	body, err := os.ReadFile(filepath.Join(dir, string(HistoryFilter)))
	require.NoError(t, err)
	require.NotContains(t, string(body), "entry-0\n",
		"the on-disk file must reflect the eviction so a re-open doesn't resurrect dropped entries")
	require.Contains(t, string(body), "entry-"+itoa(historyMaxEntries+4))

	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	require.Len(t, lines, historyMaxEntries,
		"persisted line count must match the in-memory cap")
}

// itoa is a tiny strconv-free integer-to-string helper so the test
// file imports nothing that compromises its scope.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

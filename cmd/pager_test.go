// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewPager_DisabledWhenNotTTY(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p, err := NewPager(t.Context(), &buf, false, false)
	require.NoError(t, err)
	require.Nil(t, p.cmd, "non-TTY must yield Disabled (no subprocess)")

	_, err = p.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, p.Close())
	require.Equal(t, "hello", buf.String(),
		"Disabled writes pass through to fallback unchanged")
}

func TestNewPager_DisabledWhenNoPagerSet(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	p, err := NewPager(t.Context(), &buf, true, true) // tty=true but --no-pager
	require.NoError(t, err)
	require.Nil(t, p.cmd, "--no-pager must yield Disabled even on TTY")

	_, err = p.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, p.Close())
	require.Equal(t, "x", buf.String())
}

func TestPagerFromEnv_HonoursPAGEREnv(t *testing.T) {
	// Sets PAGER for the test only; the env var is process-wide so
	// avoid t.Parallel() to prevent leaking into peers.
	t.Setenv("PAGER", "cat")

	prog, args := pagerFromEnv()
	require.Equal(t, "cat", prog)
	require.Empty(t, args, "cat takes no extra args from PAGER")
}

func TestPagerFromEnv_PAGERWithArgsSplits(t *testing.T) {
	// Multi-word PAGER env (e.g. "less -R") splits on whitespace.
	t.Setenv("PAGER", "less -R")

	prog, args := pagerFromEnv()
	require.Equal(t, "less", prog)
	require.Equal(t, []string{"-R"}, args)
}

func TestPagerFromEnv_FallbackToLessWhenAvailable(t *testing.T) {
	t.Setenv("PAGER", "")

	prog, args := pagerFromEnv()
	if prog == "" {
		t.Skip("less not on PATH on this host")
	}
	require.Equal(t, "less", prog)
	require.Equal(t, []string{"-FRX"}, args)
}

func TestPagerFromEnv_NoPATHReturnsEmpty(t *testing.T) {
	t.Setenv("PAGER", "definitely-not-on-path-zzz")
	t.Setenv("PATH", t.TempDir()) // empty PATH

	prog, args := pagerFromEnv()
	require.Empty(t, prog, "missing pager + empty PATH yields empty result")
	require.Nil(t, args)
}

func TestNewPager_SpawnsActualProcess(t *testing.T) {
	// Use `cat` as the pager — universally available, no TTY
	// requirements, echoes stdin to stdout. Lets the test exercise
	// the spawn / pipe / wait path end-to-end.
	t.Setenv("PAGER", "cat")

	var buf bytes.Buffer
	p, err := NewPager(t.Context(), &buf, true, false)
	require.NoError(t, err)
	require.NotNil(t, p.cmd, "tty=true + valid PAGER must spawn a subprocess")

	_, err = p.Write([]byte("piped content\n"))
	require.NoError(t, err)
	require.NoError(t, p.Close(),
		"clean exit (less / cat returning 0) must not surface as error")
	require.Equal(t, "piped content\n", buf.String(),
		"pager output must reach the fallback writer")
}

func TestNewPager_SpawnFailureSurfaces(t *testing.T) {
	// PAGER points to a missing program AND less is unavailable.
	t.Setenv("PAGER", "definitely-not-on-path-zzz")
	t.Setenv("PATH", t.TempDir())

	var buf bytes.Buffer
	p, err := NewPager(t.Context(), &buf, true, false)
	require.NoError(t, err, "missing pager falls back to Disabled rather than erroring")
	require.Nil(t, p.cmd, "no resolvable pager → Disabled")

	_, _ = p.Write([]byte("x"))
	require.Equal(t, "x", buf.String())
}

func TestPager_DisabledClosesIdempotently(t *testing.T) {
	t.Parallel()

	p := Disabled(os.Stdout)
	require.NoError(t, p.Close())
	require.NoError(t, p.Close(), "Close must be safe to call twice")
}

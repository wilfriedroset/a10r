// SPDX-License-Identifier: Apache-2.0

package poll

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Deterministic-curve tests reuse the noJitter Backoff already
// defined in poll_test.go.

func TestBackoff_Delay_ZeroFailuresReturnsInterval(t *testing.T) {
	t.Parallel()
	got := noJitter.Delay(0, 30*time.Second)
	require.Equal(t, 30*time.Second, got)
}

func TestBackoff_Delay_ExponentialGrowth(t *testing.T) {
	t.Parallel()
	interval := time.Minute // cap = 6 × 1m = 6m, far above the values below

	cases := []struct {
		failures int
		want     time.Duration
	}{
		{1, time.Second},     // base
		{2, 2 * time.Second}, // base × 2
		{3, 4 * time.Second}, // base × 4
		{4, 8 * time.Second}, // base × 8
	}
	for _, tc := range cases {
		got := noJitter.Delay(tc.failures, interval)
		require.Equalf(t, tc.want, got, "failures=%d", tc.failures)
	}
}

func TestBackoff_Delay_CappedAtMultiplier(t *testing.T) {
	t.Parallel()
	bo := Backoff{Base: time.Second, CapMultiplier: 2, JitterFraction: 0}
	interval := 4 * time.Second // cap = 2 × 4s = 8s

	// failures=10 → base << 9 = 512s if uncapped; expect cap.
	got := bo.Delay(10, interval)
	require.Equal(t, 8*time.Second, got)
}

func TestBackoff_Delay_LargeFailuresClampShift(t *testing.T) {
	t.Parallel()
	// Failures >30 must not panic from a negative or oversized shift —
	// the implementation clamps to <<30. Cap keeps the result sane.
	bo := Backoff{Base: time.Second, CapMultiplier: 6, JitterFraction: 0}
	got := bo.Delay(100, time.Minute)
	require.Equal(t, 6*time.Minute, got)
}

func TestBackoff_ApplyJitter_ZeroFractionPassesThrough(t *testing.T) {
	t.Parallel()
	bo := Backoff{Base: time.Second, CapMultiplier: 6, JitterFraction: 0}
	d := 10 * time.Second
	require.Equal(t, d, bo.applyJitter(d))
}

func TestBackoff_ApplyJitter_ScalesSpanByDraw(t *testing.T) {
	t.Parallel()
	d := time.Second
	cases := []struct {
		name     string
		fraction float64
		draw     float64
		want     time.Duration
	}{
		{"lowest draw subtracts the full span", 0.1, 0, 900 * time.Millisecond},
		{"midpoint draw leaves d unchanged", 0.1, 0.5, d},
		{"quarter draw subtracts half the span", 0.1, 0.25, 950 * time.Millisecond},
		{"highest draw rounds to the full span", 0.1, math.Nextafter(1, 0), 1100 * time.Millisecond},
		{"span larger than d clamps at zero", 2, 0, 0},
		{"span equal to d reaches zero", 1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bo := Backoff{JitterFraction: tc.fraction, randFloat: func() float64 { return tc.draw }}
			require.Equal(t, tc.want, bo.applyJitter(d))
		})
	}
}

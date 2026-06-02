// SPDX-License-Identifier: Apache-2.0

package poll

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Deterministic-curve tests reuse the noJitter Backoff already
// defined in poll_test.go. Random jitter is exercised end-to-end
// by TestPoller_JitterEnvelope rather than mocked at the Backoff
// layer (no caller varies on randomness).

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

func TestBackoff_ApplyJitter_StaysInEnvelope(t *testing.T) {
	t.Parallel()
	bo := Backoff{Base: time.Second, CapMultiplier: 6, JitterFraction: 0.1}
	d := time.Second
	lo := time.Duration(float64(d) * 0.9)
	hi := time.Duration(float64(d) * 1.1)
	// 1000 draws is enough for the envelope check to be useful while
	// keeping the test inexpensive. Per-draw bounds are the contract;
	// distribution shape is not asserted.
	for range 1000 {
		got := bo.applyJitter(d)
		require.GreaterOrEqual(t, got, lo)
		require.LessOrEqual(t, got, hi)
	}
}

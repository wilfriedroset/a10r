// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"strconv"
	"sync/atomic"

	"github.com/wilfriedroset/a10r/internal/backend"
)

// FakeSilenceClient is a deterministic stand-in for the silence
// write surface (silenceform.Client + ExpireSilence). Returns a
// monotonically-increasing synthetic ID per CreateSilence so fuzz
// iterations stay reproducible without stuttering on duplicate
// IDs. Concurrent-safe: the alerts page fans CreateSilence out
// across worker goroutines on bulk submit, so the counter must
// tolerate parallel access.
type FakeSilenceClient struct {
	counter atomic.Uint64
}

// CreateSilence returns "fake-N" where N is the next sequence
// number. Errors only via the typed nil — the fuzz oracle is
// panic-only and an injected error path here would just exercise
// the page's error flash, not new state.
func (f *FakeSilenceClient) CreateSilence(_ context.Context, _ backend.SilenceSpec) (string, error) {
	n := f.counter.Add(1)
	return "fake-" + strconv.FormatUint(n, 10), nil
}

// UpdateSilence accepts every spec; like CreateSilence the goal
// is to keep the fuzz path moving rather than test error paths.
func (*FakeSilenceClient) UpdateSilence(context.Context, string, backend.SilenceSpec) error {
	return nil
}

// ExpireSilence accepts every id. Lets the silences page's `x`
// binding flow through without standing up a separate fake.
func (*FakeSilenceClient) ExpireSilence(context.Context, string) error { return nil }

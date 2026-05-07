// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func TestFakeSilenceClient_CreateSilenceUniqueIDs(t *testing.T) {
	t.Parallel()
	c := &FakeSilenceClient{}
	ctx := context.Background()

	id1, err := c.CreateSilence(ctx, backend.SilenceSpec{})
	require.NoError(t, err)
	id2, err := c.CreateSilence(ctx, backend.SilenceSpec{})
	require.NoError(t, err)
	require.NotEqual(t, id1, id2, "ids must be unique across calls")
}

func TestFakeSilenceClient_ConcurrentCreateNoCollision(t *testing.T) {
	t.Parallel()
	c := &FakeSilenceClient{}
	ctx := context.Background()

	const n = 200
	ids := make(chan string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			id, err := c.CreateSilence(ctx, backend.SilenceSpec{})
			require.NoError(t, err)
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, n)
	for id := range ids {
		_, dup := seen[id]
		require.False(t, dup, "duplicate id %q under concurrent create", id)
		seen[id] = struct{}{}
	}
	require.Len(t, seen, n)
}

func TestFakeSilenceClient_UpdateAndExpireNoError(t *testing.T) {
	t.Parallel()
	c := &FakeSilenceClient{}
	ctx := context.Background()
	require.NoError(t, c.UpdateSilence(ctx, "x", backend.SilenceSpec{}))
	require.NoError(t, c.ExpireSilence(ctx, "x"))
}

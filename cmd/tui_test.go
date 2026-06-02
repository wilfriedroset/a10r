// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestExecuteContext_CancelsOnContextDone pins that cmd.Context()
// is cancellable: pages document editorCtx / bulkCtx propagate app
// shutdown, so Execute must thread signal.NotifyContext through
// rather than running with context.Background(). SIGTERM is
// exercised indirectly — signal.NotifyContext returns a regular
// cancellable Context and ExecuteContext is the only plumbing.
func TestExecuteContext_CancelsOnContextDone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	observed := make(chan context.Context, 1)
	var flags GlobalFlags
	root := newRootCmd(&flags, func(cmd *cobra.Command, _ *GlobalFlags) error {
		observed <- cmd.Context()
		return nil
	})
	root.SetArgs([]string{})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))

	// Cancel BEFORE ExecuteContext returns so the assertion below
	// sees the cancellation propagated through cmd.Context(). A
	// real SIGTERM path is the same shape.
	cancel()

	require.NoError(t, root.ExecuteContext(ctx))
	got := <-observed
	require.NotNil(t, got, "RunE must run and capture cmd.Context()")
	require.ErrorIs(t, got.Err(), context.Canceled,
		"cmd.Context() must inherit cancellation from the parent passed to ExecuteContext")
}

// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestExecuteContext_CancelsOnContextDone pins Fix 2:
// cmd.Context() must become cancellable via signal.NotifyContext +
// ExecuteContext. Pages document that editorCtx / bulkCtx parents
// propagate app shutdown — but the contract was dead because
// cmd/cmd.go called rootCmd.Execute() so cmd.Context() was always
// context.Background(). This test exercises the wiring with a
// no-op RunE and verifies that once the parent context is
// cancelled the command observes it via cmd.Context(). Real
// SIGTERM is exercised indirectly: signal.NotifyContext produces
// a context that cancels on the configured signals, and the only
// thing ExecuteContext is doing is plumbing that context through.
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

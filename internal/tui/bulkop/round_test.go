// SPDX-License-Identifier: Apache-2.0

package bulkop_test

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/bulkop"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
)

func TestBeginRoundCancelsPriorAndDerivesFromParent(t *testing.T) {
	t.Parallel()

	priorCancelled := false
	prior := context.CancelFunc(func() { priorCancelled = true })

	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	ctx, cancel := bulkop.BeginRound(parent, prior)
	defer cancel()

	require.True(t, priorCancelled, "the previous round's cancel must fire")
	require.NoError(t, ctx.Err(), "the fresh round's context starts live")

	parentCancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled,
		"cancelling the parent must propagate to the derived round context")
}

func TestBeginRoundNilParentFallsBackToBackground(t *testing.T) {
	t.Parallel()

	ctx, cancel := bulkop.BeginRound(nil, nil) //nolint:staticcheck // exercising the documented nil-parent fallback
	defer cancel()
	require.NoError(t, ctx.Err(), "nil parent must yield a live background-derived context")
}

func TestRunRoundCancelsAfterDispatch(t *testing.T) {
	t.Parallel()

	cancelled := false
	cancel := context.CancelFunc(func() { cancelled = true })
	sentinel := tea.Msg("done")
	dispatch := tea.Cmd(func() tea.Msg { return sentinel })

	cmd := bulkop.RunRound(cancel, dispatch)
	require.False(t, cancelled, "cancel must not fire before the Cmd runs")
	require.Equal(t, sentinel, cmd(), "RunRound must return the dispatch result")
	require.True(t, cancelled, "cancel must fire once dispatch returns")
}

func TestSilenceResultFlash(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                   string
		total, success, failed int
		noun                   string
		wantLevel              footer.FlashLevel
		wantText               string
	}{
		{"single success", 1, 1, 0, "alerts", footer.FlashSuccess, "silence created"},
		{"single failure", 1, 0, 1, "alerts", footer.FlashError, "silence failed"},
		{"all success alerts", 3, 3, 0, "alerts", footer.FlashSuccess, "silenced 3 alerts"},
		{"all success instances", 2, 2, 0, "instances", footer.FlashSuccess, "silenced 2 instances"},
		{"all failed", 4, 0, 4, "alerts", footer.FlashError, "silence failed for 4 alerts"},
		{"partial", 5, 3, 2, "instances", footer.FlashWarn, "silenced 3 of 5 — 2 failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := bulkop.SilenceResultFlash(tc.total, tc.success, tc.failed, tc.noun)
			msg := cmd().(footer.FlashShowMsg)
			require.Equal(t, tc.wantLevel, msg.Level)
			require.Equal(t, tc.wantText, msg.Text)
		})
	}
}

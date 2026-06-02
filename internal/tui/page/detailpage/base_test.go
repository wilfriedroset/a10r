// SPDX-License-Identifier: Apache-2.0

package detailpage_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/page/detailpage"
)

func TestBase_DefaultNoOpBubbleTeaMethods(t *testing.T) {
	t.Parallel()
	b := &detailpage.Base{}

	require.Nil(t, b.Init(), "Init must return nil when InitCmd is unwired")
	require.Nil(t, b.Close(), "Close must return nil — Base owns no lifecycle resource")
	require.Empty(t, b.HeaderContent(), "HeaderContent must be empty by default")
	require.Empty(t, b.Footer(), "Footer must be empty by default")
}

func TestBase_InitDelegatesToInitCmd(t *testing.T) {
	t.Parallel()

	sentinel := tea.Msg("init-fired")
	calls := 0
	b := &detailpage.Base{
		InitCmd: func() tea.Cmd {
			calls++
			return func() tea.Msg { return sentinel }
		},
	}

	cmd := b.Init()
	require.NotNil(t, cmd, "Init must surface the wired InitCmd")
	require.Equal(t, 1, calls, "Init must invoke the wired InitCmd exactly once")
	require.Equal(t, sentinel, cmd(), "the returned Cmd must produce the InitCmd's Msg")
}

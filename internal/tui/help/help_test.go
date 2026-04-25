// SPDX-License-Identifier: Apache-2.0

package help

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

func newRegistry(t *testing.T) *action.Registry {
	t.Helper()
	r := action.New()
	r.Register(action.Action{Key: "q", Description: "quit", View: ""})
	r.Register(action.Action{Key: "?", Description: "help", View: ""})
	r.Register(action.Action{Key: "s", Description: "silence", View: "alerts", Dangerous: true})
	r.Register(action.Action{Key: "/", Description: "filter", View: "alerts"})
	r.Register(action.Action{Key: "j", Description: "down", View: "table"})
	return r
}

func TestHelp_BucketsByLayer(t *testing.T) {
	t.Parallel()
	h := New(Options{Registry: newRegistry(t), View: "alerts"})
	out := h.View(80, 30)
	require.Contains(t, out, "Global")
	require.Contains(t, out, "[q]")
	require.Contains(t, out, "[?]")
	require.Contains(t, out, "View — alerts")
	require.Contains(t, out, "[s]")
	require.Contains(t, out, "[/]")
	require.Contains(t, out, "Table")
	require.Contains(t, out, "[j]")
}

func TestHelp_ReadOnlyHidesDangerous(t *testing.T) {
	t.Parallel()
	h := New(Options{Registry: newRegistry(t), View: "alerts", ReadOnly: true})
	out := h.View(80, 30)
	require.NotContains(t, out, "[s]",
		"Dangerous bindings must be hidden under read-only mode")
	require.Contains(t, out, "[/]",
		"non-Dangerous bindings stay visible under read-only mode")
}

func TestHelp_AnyKeyEmitsClosed(t *testing.T) {
	t.Parallel()
	h := New(Options{Registry: newRegistry(t), View: "alerts"})
	_, cmd := h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(modal.HelpClosedMsg)
	require.True(t, ok, "any keystroke must emit HelpClosedMsg")
}

func TestHelp_NonKeyMessageIsIgnored(t *testing.T) {
	t.Parallel()
	h := New(Options{Registry: newRegistry(t)})
	type custom struct{}
	_, cmd := h.Update(custom{})
	require.Nil(t, cmd)
}

func TestHelp_NilRegistry(t *testing.T) {
	t.Parallel()
	h := New(Options{})
	out := h.View(80, 5)
	require.Contains(t, out, "registry not configured")
}

func TestHelp_HelpClosedMsgImplementsResultMsg(t *testing.T) {
	t.Parallel()
	var _ modal.ResultMsg = modal.HelpClosedMsg{}
}

func TestHelp_EmptyViewLabelFallsBack(t *testing.T) {
	t.Parallel()
	h := New(Options{Registry: newRegistry(t)})
	out := h.View(80, 30)
	require.Contains(t, out, "View — (none)")
}

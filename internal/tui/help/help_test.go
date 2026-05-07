// SPDX-License-Identifier: Apache-2.0

package help

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func sampleOpts(t *testing.T) Options {
	t.Helper()
	return Options{
		PageName: "alerts",
		PageBindings: []action.Action{
			{Key: "Enter", Description: "detail"},
			{Key: "Space", Description: "mark"},
			{Key: "s", Description: "silence", Dangerous: true},
			{Key: "/", Description: "filter"},
			{Key: "Shift+S", Description: "sort severity"},
			{Key: "Shift+N", Description: "sort name"},
		},
		Globals: []action.Action{
			{Key: ":", Description: "command"},
			{Key: "/", Description: "filter"},
			{Key: "?", Description: "help"},
			{Key: "Esc", Description: "back"},
			{Key: "q", Description: "quit"},
		},
		TableMotions: []action.Action{
			{Key: "j", Description: "down"},
			{Key: "k", Description: "up"},
			{Key: "gg", Description: "top"},
			{Key: "G", Description: "bottom"},
		},
		Tenants: []string{"primary", "secondary"},
		Styles:  testutil.LoadStyles(t),
	}
}

func TestHelp_TitleIsHelp(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	require.Equal(t, "Help", h.Title(),
		"the App's outer panel reads Title() to label the border")
}

func TestHelp_ColumnsRender(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	out := testutil.StripStyle(h.View(160, 30))
	for _, col := range []string{"RESOURCE", "GENERAL", "NAVIGATION", "HOTKEYS"} {
		require.Containsf(t, out, col, "column heading %q must appear", col)
	}
}

func TestHelp_ResourceColumnListsTenantsAndPageVerbs(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	out := testutil.StripStyle(h.View(160, 30))

	// Numeric quick-switch comes from the global App layer; the
	// help renders it inside RESOURCE because it changes the
	// active scope of the resource the user is looking at.
	require.Contains(t, out, "<0>")
	require.Contains(t, out, "all")
	require.Contains(t, out, "<1>")
	require.Contains(t, out, "primary")
	require.Contains(t, out, "<2>")
	require.Contains(t, out, "secondary")

	// Page-specific verbs follow.
	require.Contains(t, out, "<Enter>")
	require.Contains(t, out, "detail")
	require.Contains(t, out, "<Space>")
	require.Contains(t, out, "mark")
}

func TestHelp_ReadOnlyHidesDangerous(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.ReadOnly = true
	h := New(opts)
	out := testutil.StripStyle(h.View(160, 30))

	require.NotContains(t, out, "silence",
		"`s silence` is Dangerous and must be hidden in read-only mode")
	require.Contains(t, out, "filter",
		"non-Dangerous bindings stay visible")
}

func TestHelp_StaticColumnsRenderCuratedEntries(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	out := testutil.StripStyle(h.View(160, 30))

	for _, want := range []string{"<:>", "command", "<?>", "help", "<Esc>", "back"} {
		require.Containsf(t, out, want, "GENERAL column must surface %q", want)
	}
	for _, want := range []string{"<j>", "down", "<gg>", "top", "<G>", "bottom"} {
		require.Containsf(t, out, want, "NAVIGATION column must surface %q", want)
	}
	for _, want := range []string{"<Shift+S>", "sort severity"} {
		require.Containsf(t, out, want, "HOTKEYS column must surface %q", want)
	}
}

func TestHelp_AnyKeyEmitsClosed(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	_, cmd := h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(modal.HelpClosedMsg)
	require.True(t, ok, "any keystroke must emit HelpClosedMsg")
}

func TestHelp_NonKeyMessageIsIgnored(t *testing.T) {
	t.Parallel()
	h := New(sampleOpts(t))
	type custom struct{}
	_, cmd := h.Update(custom{})
	require.Nil(t, cmd)
}

func TestHelp_HelpClosedMsgImplementsResultMsg(t *testing.T) {
	t.Parallel()
	var _ modal.ResultMsg = modal.HelpClosedMsg{}
}

func TestHelp_NumericListClampsAtNine(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	// Twelve configured backends — the catalog only goes to 9
	// (the digit budget of the keyboard's number row).
	opts.Tenants = []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l",
	}
	h := New(opts)
	out := testutil.StripStyle(h.View(200, 40))

	require.Contains(t, out, "<9>")
	require.NotContains(t, out, "<10>",
		"numeric quick-switch tops out at <9>; extras are reachable via Ctrl+T")
}

func TestHelp_NoTenantsDropsNumericBlock(t *testing.T) {
	t.Parallel()
	opts := sampleOpts(t)
	opts.Tenants = nil
	h := New(opts)
	out := testutil.StripStyle(h.View(160, 30))

	require.NotContains(t, out, "<0>",
		"empty tenant list drops the numeric block entirely — "+
			"otherwise `<0> all` reads as a no-op key")
	require.Contains(t, out, "RESOURCE",
		"the column heading still renders so the page verbs have a banner")
}

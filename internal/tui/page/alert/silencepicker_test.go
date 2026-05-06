// SPDX-License-Identifier: Apache-2.0

package alert

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/modal"
)

// TestSilencePicker_EnterEmitsSilenceSelectedMsg covers the happy
// path: wrapper forwards Enter to the inner Picker, intercepts the
// PickerSubmittedMsg, and translates it into a typed
// SilenceSelectedMsg carrying the silence ID — NOT the rendered
// row text. This is what isolates the alert detail page's drill-
// down from the App's tenant-picker branch (app.go's
// PickerSubmittedMsg path is hard-coded to scope changes; sending
// a different result type sidesteps that).
func TestSilencePicker_EnterEmitsSilenceSelectedMsg(t *testing.T) {
	t.Parallel()

	rows := []silencePickerRow{
		{id: "abc-1", line: "abc-1  expires in 2h  by alice  — investigating spike"},
		{id: "def-2", line: "def-2  expires in 4d  by bob    — planned maintenance"},
	}
	w := newSilencePicker(rows)

	_, cmd := w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "Enter must produce a Cmd from the wrapper")
	got, ok := cmd().(SilenceSelectedMsg)
	require.True(t, ok, "wrapper must emit SilenceSelectedMsg, not the inner PickerSubmittedMsg — App.go would otherwise reroute it as a tenant scope change")
	require.Equal(t, "abc-1", got.ID,
		"selection must surface the silence ID, not the rendered row text")
}

func TestSilencePicker_DownThenEnterReturnsSecondID(t *testing.T) {
	t.Parallel()

	rows := []silencePickerRow{
		{id: "abc-1", line: "abc-1  …"},
		{id: "def-2", line: "def-2  …"},
	}
	w := newSilencePicker(rows)
	_, _ = w.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := w.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := cmd().(SilenceSelectedMsg)
	require.Equal(t, "def-2", got.ID)
}

func TestSilencePicker_EscEmitsSilenceCancelledMsg(t *testing.T) {
	t.Parallel()

	rows := []silencePickerRow{{id: "abc-1", line: "abc-1  …"}}
	w := newSilencePicker(rows)

	_, cmd := w.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.NotNil(t, cmd)
	_, ok := cmd().(SilenceCancelledMsg)
	require.True(t, ok, "Esc must emit SilenceCancelledMsg")
}

func TestSilencePicker_ResultTypesImplementModalResult(t *testing.T) {
	t.Parallel()

	// Compile-time guard: the App's auto-close path keys on
	// modal.ResultMsg, so dropping the marker on either result type
	// would silently leave the modal open after the user picks.
	// This isn't a runtime test — the build already enforces
	// satisfaction — it exists so a future refactor that retypes
	// these messages can't drop the marker without breaking a test.
	var _ modal.ResultMsg = SilenceSelectedMsg{}
	var _ modal.ResultMsg = SilenceCancelledMsg{}
}

func TestSilencePicker_TitleIsSilences(t *testing.T) {
	t.Parallel()
	// Title is rendered in the App's outer panel border. Distinct
	// from "tenants" so a future title-aware dispatcher could
	// distinguish — but more importantly, "silences" reads as the
	// thing the user is picking from.
	w := newSilencePicker([]silencePickerRow{{id: "x", line: "x"}})
	require.Equal(t, "silences", w.Title())
}

// TestSilencePicker_ViewRendersRows is a smoke test: the wrapper's
// View must delegate to the inner Picker so the rendered rows are
// the prepared single-line summaries, not raw IDs.
func TestSilencePicker_ViewRendersRows(t *testing.T) {
	t.Parallel()
	rows := []silencePickerRow{
		{id: "abc-1", line: "abc-1  expires in 2h  by alice  — go"},
	}
	w := newSilencePicker(rows)
	out := w.View(80, 10)
	require.Contains(t, out, "expires in 2h")
	require.Contains(t, out, "by alice")
}

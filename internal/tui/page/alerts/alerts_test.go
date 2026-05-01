// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// fixedNow returns a deterministic clock for the age column tests.
var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

// stripStyle drops ANSI SGR sequences for substring assertions.
func stripStyle(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// loadStyles returns a populated theme.Styles for body width
// rendering. Most assertions strip styles before comparing, so the
// exact skin doesn't matter.
func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

// alert builds a synthetic Alert for tests. Age is fixed at one
// minute so age-column rendering is consistent across the suite;
// the formatAge helper has its own dedicated test for age math.
func mkAlert(name, severity string, state backend.AlertState) backend.Alert {
	return backend.Alert{
		Labels: map[string]string{
			"alertname": name,
			"severity":  severity,
		},
		State:    state,
		StartsAt: fixedNow.Add(-time.Minute),
	}
}

func newPage(t *testing.T) *Page {
	t.Helper()
	return New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
}

func TestPage_SeverityCellWearsThemeColour(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	p := New(Options{Styles: styles, Now: func() time.Time { return fixedNow }})
	alerts := []backend.Alert{
		mkAlert("CritOne", "critical", backend.AlertStateActive),
		mkAlert("WarnTwo", "warning", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	// Move the cursor off both rows so neither inherits the cursor
	// row-level style precedence (kept per Q1.2). With two alerts,
	// the cursor sits on row 0 by default, so we walk it past the
	// last row — clamped to the last index — and assert against the
	// other row instead.
	p.cursor = 0 // critical at index 0 (severity DESC default)

	out := p.View(120, 20)
	wantWarn := styles.Severity.Warning.Render("warning")
	require.Contains(t, out, wantWarn,
		"non-cursor warning cell must carry Severity.Warning ANSI")
}

func TestPage_CursorRowSkipsSeverityColour(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	p := New(Options{Styles: styles, Now: func() time.Time { return fixedNow }})
	alerts := []backend.Alert{
		mkAlert("CritOne", "critical", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	// Single row → cursor sits on it.
	require.Equal(t, 0, p.cursor)

	out := p.View(120, 20)
	notWanted := styles.Severity.Critical.Render("critical")
	require.NotContains(t, out, notWanted,
		"cursor row must not carry the per-cell severity ANSI; row-level style wins per Q1.2")
}

func TestPage_MarkedRowSkipsSeverityColour(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	p := New(Options{Styles: styles, Now: func() time.Time { return fixedNow }})
	alerts := []backend.Alert{
		mkAlert("CritOne", "critical", backend.AlertStateActive),
		mkAlert("WarnTwo", "warning", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	// Move cursor to row 1 so row 0 isn't the cursor; mark row 0.
	p.cursor = 1
	p.snapshotFocus()
	p.marks[p.view[0].a.Fingerprint] = struct{}{}

	out := p.View(120, 20)
	notWanted := styles.Severity.Critical.Render("critical")
	require.NotContains(t, out, notWanted,
		"marked row must not carry per-cell severity ANSI; marked-row fg wins")
}

func TestPage_SuppressedRowSkipsSeverityColour(t *testing.T) {
	t.Parallel()

	styles := loadStyles(t)
	p := New(Options{Styles: styles, Now: func() time.Time { return fixedNow }})
	alerts := []backend.Alert{
		mkAlert("CritOne", "critical", backend.AlertStateActive),
		mkAlert("WarnTwo", "warning", backend.AlertStateSuppressed),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	// Cursor on row 0 (critical/active); row 1 (warning/suppressed)
	// should be dim-styled and skip per-cell colour.
	p.cursor = 0

	out := p.View(120, 20)
	notWanted := styles.Severity.Warning.Render("warning")
	require.NotContains(t, out, notWanted,
		"suppressed (dimmed) row must not carry per-cell severity ANSI")
}

func TestPage_DataMsgPopulatesView(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts, Tenant: "prod"})

	out := stripStyle(p.View(120, 20))
	require.Contains(t, out, "HighCPU")
	require.Contains(t, out, "critical")
}

func TestPage_DataMsgWrongTypeIsNoOp(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: "not alerts"})
	require.Empty(t, p.byTenant[""])
}

func TestPage_SortBySeverityPutsCriticalFirst(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("WarnFoo", "warning", backend.AlertStateActive),
		mkAlert("CritBar", "critical", backend.AlertStateActive),
		mkAlert("InfoBaz", "info", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	require.Equal(t, "CritBar", p.view[0].a.Labels["alertname"])
	require.Equal(t, "WarnFoo", p.view[1].a.Labels["alertname"])
	require.Equal(t, "InfoBaz", p.view[2].a.Labels["alertname"])
}

func TestPage_FilterSubstringAppliesAcrossLabels(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive),
		mkAlert("DiskSpace", "warning", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Filter via the prompt-submitted contract.
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "disk"})
	require.Len(t, p.view, 1)
	require.Equal(t, "DiskSpace", p.view[0].a.Labels["alertname"])
}

func TestPage_FilterPromptIsLiveAndClearsOnOpen(t *testing.T) {
	t.Parallel()

	// Two alerts; an existing filter is active.
	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive),
		mkAlert("DiskSpace", "warning", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "disk"})
	require.Len(t, p.view, 1)

	// Pressing `/` clears the active filter so the user types
	// against the unfiltered list. The pre-prompt value is
	// snapshotted so Esc can still roll back.
	_, _ = p.Update(footer.PromptOpenedMsg{Mode: footer.PromptFilter})
	require.Len(t, p.view, 2,
		"opening the filter prompt clears the active filter so live "+
			"typing rebuilds it from scratch")

	// Each keystroke trims the view live — no Enter required.
	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "h"})
	require.Len(t, p.view, 1)
	require.Equal(t, "HighCPU", p.view[0].a.Labels["alertname"])

	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "hi"})
	require.Len(t, p.view, 1)

	// Backspacing in the prompt fires Changed too — view widens
	// back as characters are removed.
	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: ""})
	require.Len(t, p.view, 2,
		"emptying the prompt live must widen the view to all rows")

	// Submit empty → filter cleared; pre-filter snapshot dropped.
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: ""})
	require.Empty(t, p.filter)
	require.Len(t, p.view, 2)
	require.Nil(t, p.preFilter,
		"empty submit commits the cleared filter and drops the snapshot")
}

func TestPage_FilterCancelRestoresPreFilter(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive),
		mkAlert("DiskSpace", "warning", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "disk"})
	require.Len(t, p.view, 1)

	// Open a new filter prompt → snapshot pre-filter state.
	_, _ = p.Update(footer.PromptOpenedMsg{Mode: footer.PromptFilter})
	// User cancels; prior filter restores.
	_, _ = p.Update(footer.PromptCancelledMsg{Mode: footer.PromptFilter})
	require.Len(t, p.view, 1, "cancelling restores the prior filter, not full list")
	require.Equal(t, "disk", p.filter)
}

func TestPage_VimMotionsMoveCursor(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("A", "critical", backend.AlertStateActive),
		mkAlert("B", "warning", backend.AlertStateActive),
		mkAlert("C", "info", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, 2, p.cursor, "G must jump to the last visible row")
}

func TestPage_CursorClampsOnEmptyView(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 0, p.cursor, "cursor must not advance past the (empty) view")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, 0, p.cursor)
}

func TestPage_StateFilterCycle(t *testing.T) {
	t.Parallel()

	// State-filter cycle moved from `t` to `Shift+F` so the
	// app-global `t` (time-format toggle) doesn't shadow it via
	// the dispatcher precedence stack.
	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("A", "critical", backend.AlertStateActive),
		mkAlert("B", "warning", backend.AlertStateSuppressed),
		mkAlert("C", "info", backend.AlertStateUnprocessed),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	require.Len(t, p.view, 3)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'F', Text: "F", Mod: tea.ModShift})
	require.Equal(t, string(backend.AlertStateActive), p.stateFilter)
	require.Len(t, p.view, 1)
	require.Equal(t, "A", p.view[0].a.Labels["alertname"])

	_, _ = p.Update(tea.KeyPressMsg{Code: 'F', Text: "F", Mod: tea.ModShift})
	require.Equal(t, string(backend.AlertStateSuppressed), p.stateFilter)
	require.Len(t, p.view, 1)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'F', Text: "F", Mod: tea.ModShift})
	require.Equal(t, string(backend.AlertStateUnprocessed), p.stateFilter)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'F', Text: "F", Mod: tea.ModShift})
	require.Empty(t, p.stateFilter, "fourth Shift+F cycles back to all")
	require.Len(t, p.view, 3)
}

func TestPage_TKeyDoesNotCycleStateFilter(t *testing.T) {
	t.Parallel()

	// The page-local `t` handler is dead code — the app-global
	// `t` (time-format toggle) consumes the key at the dispatcher
	// before the page's Update sees it. Pinning the contract: a
	// `t` keypress reaching the page directly (legacy callers,
	// tests) must NOT cycle state filters.
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("A", "critical", backend.AlertStateActive),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Empty(t, p.stateFilter,
		"`t` is owned by the app-global time-format toggle; the page must not bind it")
}

func TestPage_SortColumnWalk(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, SortBySeverity, p.sort)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, SortByName, p.sort)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, SortBySeverity, p.sort)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, SortByAge, p.sort, "left walk wraps to the rightmost column")
}

func TestPage_SilenceKeyOnEmptyViewFlashesHint(t *testing.T) {
	t.Parallel()

	// Clients set so the "no writeable backend" path doesn't win;
	// no DataMsg → empty view.
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no alert under the cursor")
}

func TestPage_SilenceKeyWithoutClientsFlashesHint(t *testing.T) {
	t.Parallel()

	p := newPage(t) // no Clients configured
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("HighCPU", "critical", backend.AlertStateActive)},
		Tenant:   "prod",
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend",
		"`s` with no clients must explain rather than push a broken form")
}

func TestPage_SilenceKeyPushesFormWhenClientsAreConfigured(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
		Creator: "wilfried",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("HighCPU", "critical", backend.AlertStateActive)},
		Tenant:   "prod",
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd, "`s` with clients must push the form")
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "`s` with clients must push the form, not flash")
}

func TestPage_SilenceFormSubmittedFlashesSuccess(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, cmd := p.Update(silenceform.SubmittedMsg{ID: "sil-99"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence created: sil-99")
}

// fakeSilenceClient is the smallest silenceform.Client a test
// needs. The alerts test only asserts `s` produces a non-flash
// Cmd; CreateSilence is never called from within the page.
type fakeSilenceClient struct{}

func (*fakeSilenceClient) CreateSilence(_ context.Context, _ backend.SilenceSpec) (string, error) {
	return "fake-silence-id", nil
}

func (*fakeSilenceClient) UpdateSilence(_ context.Context, _ string, _ backend.SilenceSpec) error {
	return nil
}

func TestPage_CursorPreservedAcrossDataRefresh(t *testing.T) {
	t.Parallel()

	// Track cursor by fingerprint, not index. A poll tick that
	// reorders alerts must not slide the cursor onto a different
	// alert.
	p := newPage(t)
	withFP := func(name, fp string) backend.Alert {
		a := mkAlert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	first := []backend.Alert{
		withFP("A", "fp-a"),
		withFP("B", "fp-b"),
		withFP("C", "fp-c"),
	}
	_, _ = p.Update(poll.DataMsg{Resource: first})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "fp-b", p.view[p.cursor].a.Fingerprint)

	// New tick: B has shifted to the bottom (new alerts inserted
	// above it). Cursor must follow B.
	second := []backend.Alert{
		withFP("X", "fp-x"),
		withFP("Y", "fp-y"),
		withFP("A", "fp-a"),
		withFP("B", "fp-b"),
	}
	_, _ = p.Update(poll.DataMsg{Resource: second})
	require.Equal(t, "fp-b", p.view[p.cursor].a.Fingerprint,
		"cursor must follow the focused alert across poll refreshes")
}

func TestPage_CursorClampsWhenFocusedAlertGone(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	withFP := func(name, fp string) backend.Alert {
		a := mkAlert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		withFP("A", "fp-a"),
		withFP("B", "fp-b"),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "fp-b", p.view[p.cursor].a.Fingerprint)

	// B is gone; cursor must clamp to the last remaining row.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		withFP("A", "fp-a"),
	}})
	require.Equal(t, 0, p.cursor)
}

func TestPage_EnterDrillsToDetail(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	a := mkAlert("HighCPU", "critical", backend.AlertStateActive)
	a.Fingerprint = "fp-cpu"
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{a}})

	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "Enter on a populated row must produce a push Cmd")
	// Running the Cmd yields the pushPageMsg the App's Update consumes.
	// We can't assert against the (unexported) pushPageMsg type from this
	// package; the contract is covered by app.PushPage's own tests, so
	// here we just lock that a Cmd was returned and that it doesn't
	// produce a flash.
	msg := cmd()
	if _, isFlash := msg.(footer.FlashShowMsg); isFlash {
		t.Fatalf("Enter on a populated row must NOT flash; got %#v", msg)
	}
}

func TestPage_EnterOnEmptyViewFlashesHint(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "no alert")
}

func TestPage_SpaceTogglesMarkAndHeaderShowsCount(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	mk := func(name, fp string) backend.Alert {
		a := mkAlert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mk("A", "fp-a"),
		mk("B", "fp-b"),
	}})

	require.Empty(t, p.marks)
	require.NotContains(t, p.HeaderContent(), "marked:")

	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Contains(t, p.marks, "fp-a")
	require.Contains(t, p.HeaderContent(), "marked:1")

	// Space again on the same row clears the mark.
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.NotContains(t, p.marks, "fp-a")
}

func TestPage_MarkedRowCarriesMarkedStyle(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	mk := func(name, fp string) backend.Alert {
		a := mkAlert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mk("First", "fp-1"),
		mk("Second", "fp-2"),
	}})

	// Move cursor off row 0, then mark row 0. Row 0 must wear
	// the Marked style; the cursor row (now row 1) wears Cursor.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})

	// Render and locate the row 0 line. Marked rows wear ANSI
	// styling (foreground-only — the background stays the default
	// so the row doesn't compete visually with the cursor stripe)
	// and the marker column shows the check glyph.
	out := p.View(120, 20)
	rowLines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(rowLines), 3)
	row0 := rowLines[1] // header at [0], rows start at [1]
	require.Contains(t, row0, "\x1b[",
		"a marked non-cursor row must carry ANSI styling")
	require.NotContains(t, row0, "\x1b[48;",
		"marked rows must NOT change the background — only the foreground")
	require.Contains(t, stripStyle(row0), "✓",
		"the marker column must show the check glyph on marked rows")
}

func TestPage_MarksRenderedNextToCursor(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	mk := func(name, fp string) backend.Alert {
		a := mkAlert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mk("First", "fp-1"),
		mk("Second", "fp-2"),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})

	out := stripStyle(p.View(120, 20))
	require.Contains(t, out, "✓",
		"marked rows must show a check glyph in the marker column")
}

func TestPage_GoToFirstRowResetsCursorAndScroll(t *testing.T) {
	t.Parallel()

	alerts := make([]backend.Alert, 30)
	for i := range alerts {
		alerts[i] = mkAlert(fmt.Sprintf("Alert%02d", i), "warning", backend.AlertStateActive)
		alerts[i].Fingerprint = fmt.Sprintf("fp-%02d", i)
	}
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Walk the cursor far down, then send the chord-resolved msg.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Positive(t, p.cursor)

	_, _ = p.Update(app.GoToFirstRowMsg{})
	require.Equal(t, 0, p.cursor,
		"GoToFirstRowMsg must move the cursor to the top")

	// Force a render so reconcileScroll runs and topRow is reset.
	_ = p.View(80, 10)
	require.Equal(t, 0, p.topRow,
		"top of the table must be in view after GoToFirstRow + render")
}

func TestPage_ViewportFollowsCursor(t *testing.T) {
	t.Parallel()

	// 30 alerts, body just tall enough for ~5 rows. After walking
	// the cursor down past the visible window the view must scroll
	// so the cursor row is rendered.
	alerts := make([]backend.Alert, 30)
	for i := range alerts {
		alerts[i] = mkAlert(fmt.Sprintf("Alert%02d", i), "warning", backend.AlertStateActive)
		alerts[i].Fingerprint = fmt.Sprintf("fp-%02d", i)
	}
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Walk the cursor 20 rows down (past any reasonable viewport).
	for range 20 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}

	// Render at width=80, height=10 (≈7-8 rows visible after the
	// header and footer). The cursor row's alertname must appear.
	out := stripStyle(p.View(80, 10))
	require.Contains(t, out, p.view[p.cursor].a.Labels["alertname"],
		"viewport must scroll so the cursor row stays visible")

	// G jumps to the last row; the bottom of the list must be in
	// view (the page scrolled all the way down).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	out = stripStyle(p.View(80, 10))
	require.Contains(t, out, "Alert29",
		"G must scroll the viewport to the last row")

	// gg-equivalent: cursor back to 0 → top-of-list visible again.
	for range 30 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	out = stripStyle(p.View(80, 10))
	require.Contains(t, out, "Alert00",
		"walking back up must scroll the viewport to the top")
}

func TestPage_CursorRowIsHighlighted(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("First", "critical", backend.AlertStateActive),
		mkAlert("Second", "warning", backend.AlertStateActive),
	}})

	out := p.View(120, 20)
	// The cursor row gets wrapped in the Table.Cursor style. The
	// catppuccin-mocha skin maps that to a lavender-on-base ANSI
	// sequence; tests don't pin the colour values (the skin can
	// change), but the cursor row MUST carry an ANSI escape — and
	// MUST NOT carry the per-cell severity ANSI (Q1.2: row-level
	// style wins over per-cell colouring).
	lines := strings.Split(stripStyle(out), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	cursorLine := strings.SplitN(out, "\n", 4)[1] // header → row 0 (cursor) → ...
	otherLine := strings.SplitN(out, "\n", 4)[2]
	require.Contains(t, cursorLine, "\x1b[",
		"the cursor row must carry ANSI styling")
	// Non-cursor rows do carry per-cell severity ANSI now. Assert
	// the Cursor style ANSI is absent — that's the contract that
	// keeps the cursor visually distinct from a coloured cell.
	styles := loadStyles(t)
	cursorANSI := styles.Table.Cursor.Render("x")
	cursorPrefix := strings.SplitN(cursorANSI, "x", 2)[0]
	require.NotContains(t, otherLine, cursorPrefix,
		"non-cursor rows must not carry the cursor row-level style")
}

func TestPage_BindingsIncludeEnterDrill(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	var sawEnter bool
	for _, b := range p.Bindings() {
		if b.Key == "Enter" {
			sawEnter = true
			require.False(t, b.Dangerous, "drill must NOT be Dangerous")
		}
	}
	require.True(t, sawEnter, "alerts page must surface Enter→detail in its hints")
}

func TestPage_DirectSortShortcuts(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.Equal(t, SortByName, p.sort,
		"Shift+N must sort by alertname directly (no walk)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'A', Text: "A", Mod: tea.ModShift})
	require.Equal(t, SortByAge, p.sort)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S", Mod: tea.ModShift})
	require.Equal(t, SortBySeverity, p.sort)
}

func TestPage_SortShortcutTogglesDirection(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// Severity starts active, descending (critical first).
	require.Equal(t, SortBySeverity, p.sort)
	require.False(t, p.sortAsc)

	// Pressing the active column's shortcut flips the direction.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.Equal(t, SortBySeverity, p.sort)
	require.True(t, p.sortAsc, "second Shift+S must flip to ascending")

	// A third press flips back to descending.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.False(t, p.sortAsc, "third Shift+S must flip back to descending")

	// Switching to a different column resets to that column's
	// default direction (ascending for non-severity columns).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"})
	require.Equal(t, SortByName, p.sort)
	require.True(t, p.sortAsc, "switching column resets to default direction")

	// Pressing the new column's shortcut flips it.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"})
	require.False(t, p.sortAsc, "second Shift+N must flip alertname to descending")
}

func TestPage_HLWalkResetsDirection(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// Flip severity to ascending first.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.True(t, p.sortAsc)

	// l walks to the next column (Name) — must reset direction
	// to the new column's default (ascending), regardless of
	// what the previous column's direction was.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, SortByName, p.sort)
	require.True(t, p.sortAsc)

	// h walks back. Severity's default is descending.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, SortBySeverity, p.sort)
	require.False(t, p.sortAsc,
		"walking back to severity must reset to its default (descending)")
}

func TestPage_TenantColumnAppearsForAllScope(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
		Scope:  "all",
	})

	// Two backends emit DataMsgs — TENANT column kicks in.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("A", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("B", "warning", backend.AlertStateActive)},
		Tenant:   "staging",
	})

	out := stripStyle(p.View(140, 20))
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "staging")
}

func TestPage_TenantColumnHiddenForSingleBackend(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
		Scope:  "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("A", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})

	out := stripStyle(p.View(140, 20))
	require.NotContains(t, out, "TENANT",
		"single-backend setups must NOT show the tenant column")
}

func TestPage_TitleIncludesScope(t *testing.T) {
	t.Parallel()

	// Default scope (empty) reads as "all" — k9s convention. Drive
	// the page out of cold-start with an empty in-scope DataMsg
	// before checking the title; pre-poll the title flips to the
	// loading affordance.
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{}, Tenant: ""})
	require.Equal(t, "alerts(all)[0]", p.Title())

	// Explicit scope from Options threads into the title.
	p2 := New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
		Scope:  "prod",
	})
	_, _ = p2.Update(poll.DataMsg{Resource: []backend.Alert{}, Tenant: "prod"})
	require.Equal(t, "alerts(prod)[0]", p2.Title())

	// SetScope updates the active label live.
	p2.SetScope("prod,staging")
	require.Equal(t, "alerts(prod,staging)[0]", p2.Title())
}

func TestPage_TitleColdStartShowsLoading(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	out := stripStyle(p.Title())
	require.Contains(t, out, "loading alerts",
		"cold-start title must read as loading until the first DataMsg lands")
}

func TestPage_TitleAfterDataMsgFlipsToCount(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("A", "warning", backend.AlertStateActive)},
		Tenant:   "",
	})
	require.Equal(t, "alerts(all)[1]", p.Title(),
		"first in-scope DataMsg must drop the loading affordance")
}

func TestPage_RefreshKeyEmitsRequestAndFlipsRefreshing(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// First, get the page into the polled state so the title's
	// "refreshing" branch is observable as a flip.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{}, Tenant: ""})
	require.False(t, p.refreshing)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.True(t, p.refreshing,
		"`r` must flip the page into refreshing state")

	// The Cmd is a tea.Batch carrying the RefreshRequestedMsg and
	// the spinner Tick. Walk the batch and assert the Refresh
	// payload is present.
	require.NotNil(t, cmd)
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok, "Cmd must produce a BatchMsg")
	var sawRefresh bool
	for _, c := range batch {
		if m := c(); m != nil {
			if rr, ok := m.(app.RefreshRequestedMsg); ok {
				require.Equal(t, "alerts", rr.Resource)
				require.Equal(t, "all", rr.Scope)
				sawRefresh = true
			}
		}
	}
	require.True(t, sawRefresh,
		"Batch must contain RefreshRequestedMsg{Resource:alerts}")
}

func TestPage_TimeFormatToggleSwitchesAgeColumn(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// Pin StartsAt to a known absolute time so the absolute label
	// is deterministic across runs / hosts. fixedNow is in UTC;
	// header.FormatAbsolute renders in local time, so the test
	// asserts only the date portion which is timezone-stable
	// within a few hours of UTC.
	startsAt := fixedNow.Add(-time.Minute)
	a := mkAlert("CritOne", "critical", backend.AlertStateActive)
	a.StartsAt = startsAt
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{a}, Tenant: ""})

	// Default mode is relative — body shows "1m ago".
	out := stripStyle(p.View(140, 20))
	require.Contains(t, out, "1m ago")
	require.NotContains(t, out, "2026-",
		"relative mode must not surface the absolute date")

	// Flip to absolute — body shows the ISO local stamp. Per
	// post-batch UX call (max real-estate), the time mode is NOT
	// surfaced in HeaderContent; the toggle's flash is the
	// affordance signal and the cell content speaks for itself.
	_, _ = p.Update(app.TimeFormatChangedMsg{Format: app.TimeFormatAbsolute})
	out = stripStyle(p.View(140, 20))
	require.NotContains(t, out, "1m ago")
	require.Contains(t, out, "2026-",
		"absolute mode must surface the ISO local date prefix")
	require.NotContains(t, p.HeaderContent(), "time:",
		"time mode must NOT take a HeaderContent slot — saves a body row")
}

func TestPage_FooterShowsRefreshingThenNextRefresh(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Empty(t, p.Footer(),
		"pre-poll Footer is empty so the cold-start frame stays quiet")

	next := fixedNow.Add(25 * time.Second)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{},
		Tenant:   "",
		NextAt:   next,
	})
	require.Equal(t, "next refresh 25s", p.Footer())

	_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Equal(t, "refreshing…", p.Footer(),
		"manual `r` flips the bottom border to the refreshing affordance")

	// Once the next DataMsg lands, the timer reads naturally again.
	later := fixedNow.Add(40 * time.Second)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{},
		Tenant:   "",
		NextAt:   later,
	})
	require.Equal(t, "next refresh 40s", p.Footer())
}

func TestPage_ScopeChangedMsgFiltersAndUpdatesTitle(t *testing.T) {
	t.Parallel()

	// Two backends each report one alert. Scope starts as "all".
	p := New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
		Scope:  "all",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("A", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("B", "warning", backend.AlertStateActive)},
		Tenant:   "staging",
	})

	require.Equal(t, "alerts(all)[2]", p.Title())
	out := stripStyle(p.View(140, 20))
	require.Contains(t, out, "A", "all-scope view shows both alerts")
	require.Contains(t, out, "B")
	require.Contains(t, out, "TENANT",
		"two tenants in scope keeps the TENANT column visible")

	// `<1>` quick-switch arrives via the bubbletea bus.
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "prod"})
	require.Equal(t, "alerts(prod)[1]", p.Title(),
		"scope change must rescope the [N] count, not just the label")
	out = stripStyle(p.View(140, 20))
	require.Contains(t, out, "A",
		"prod's alert stays visible after the scope switch")
	require.NotContains(t, out, "B",
		"alerts from out-of-scope tenants must drop out of the view")
	require.NotContains(t, out, "TENANT",
		"single-tenant scope hides the TENANT column even when "+
			"other tenants are still in byTenant")

	// `<0>` returns to all-tenants — count and column reappear.
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "all"})
	require.Equal(t, "alerts(all)[2]", p.Title())
	out = stripStyle(p.View(140, 20))
	require.Contains(t, out, "TENANT")
}

func TestPage_CrumbAndHeader(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, "alerts", p.Crumb())
	// HeaderContent is empty when no filter / state filter / marks
	// are active — the column header arrow already shows the sort
	// state, repeating it as a subtitle is noise.
	require.Empty(t, p.HeaderContent())

	// Once a filter kicks in, the subtitle re-appears.
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "high"})
	require.Contains(t, p.HeaderContent(), "filter:high")
}

func TestPage_BindingsContainSilenceAsDangerous(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	bindings := p.Bindings()
	var sawSilence bool
	for _, a := range bindings {
		if a.Key == "s" {
			sawSilence = true
			require.True(t, a.Dangerous,
				"silence binding must carry Dangerous so read-only mode hides it")
		}
	}
	require.True(t, sawSilence, "alerts page must expose `s` silence binding")
}

func TestPage_EmptyStateMessages(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	out := stripStyle(p.View(80, 5))
	require.Contains(t, out, "no alerts (yet)",
		"with no data and no filter the empty state explains polling")

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "nope"})
	out = stripStyle(p.View(80, 5))
	require.Contains(t, out, "no alerts match",
		"with a non-matching filter the empty state hints at clearing it")
}

// stylePrefix returns the SGR prefix the renderer emits for the
// supplied style. Used by the row-style assertions below to detect
// which branch fired without coupling to a specific colour value
// (skin updates change the colour but not the structural shape).
func stylePrefix(t *testing.T, rendered string) string {
	t.Helper()
	idx := strings.Index(rendered, "x")
	require.GreaterOrEqual(t, idx, 0,
		"render probe must contain the literal probe character")
	return rendered[:idx]
}

// TestPage_SuppressedRowsRenderDimmed exercises the third arm of
// the row-style switch (cursor > marked > dimmed). The existing
// stripStyle path erases SGR sequences, so the assertion compares
// against a freshly-rendered probe through the same Dimmed style:
// if the row contains the probe's SGR prefix, the dimmed branch
// fired.
func TestPage_SuppressedRowsRenderDimmed(t *testing.T) {
	t.Parallel()
	// One active row at row 0 (holds the cursor) and one suppressed
	// row at row 1 — the latter is the row whose dimmed style we're
	// asserting on.
	p := newPage(t)
	active := mkAlert("Firing", "critical", backend.AlertStateActive)
	suppressed := mkAlert("Silenced", "warning", backend.AlertStateSuppressed)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{active, suppressed}})

	out := p.View(120, 10)
	require.Contains(t, stripStyle(out), "Silenced",
		"sanity: the suppressed row still renders its label")

	// The renderer extracts only the foreground from
	// theme.Table.Dimmed (so the row keeps the body bg, matching
	// the Marked branch). Build the probe the same way so the SGR
	// prefix matches.
	probeDimmed := lipgloss.NewStyle().
		Foreground(p.styles.Table.Dimmed.GetForeground()).
		Render("x")
	require.Contains(t, out, stylePrefix(t, probeDimmed),
		"suppressed-only row must wear the Table.Dimmed foreground colour "+
			"with the body background unchanged (no second highlighted stripe)")
}

func TestPage_MarkedSuppressedRowKeepsMarkedStyle(t *testing.T) {
	t.Parallel()
	// Marked beats dimmed: an explicit user action wins over the
	// ambient suppression-state hint. Need a second row so the
	// cursor can leave the marked row (otherwise cursor wins over
	// both marked and dimmed).
	p := newPage(t)
	active := mkAlert("Firing", "critical", backend.AlertStateActive)
	active.Fingerprint = "fp-active"
	suppressed := mkAlert("Silenced", "warning", backend.AlertStateSuppressed)
	suppressed.Fingerprint = "fp-supp"
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{active, suppressed}})
	// SortBySeverity desc: critical at row 0, warning at row 1.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // cursor → row 1
	_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "}) // mark suppressed
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"}) // cursor → row 0

	// The renderer extracts only the foreground from
	// theme.Table.Marked (so the row keeps the body bg). Build the
	// probe the same way so the SGR prefix matches.
	out := p.View(120, 10)
	probeMarked := lipgloss.NewStyle().
		Foreground(p.styles.Table.Marked.GetForeground()).
		Render("x")
	require.Contains(t, out, stylePrefix(t, probeMarked),
		"marked + suppressed must render in the marked style, not dimmed")
}

func TestPage_CursorOnSuppressedRowKeepsCursorStyle(t *testing.T) {
	t.Parallel()
	// Cursor beats both marked and dimmed.
	p := newPage(t)
	suppressed := mkAlert("Silenced", "warning", backend.AlertStateSuppressed)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{suppressed}})
	// Cursor is on row 0 by default.

	out := p.View(120, 10)
	require.Contains(t, out, stylePrefix(t, p.styles.Table.Cursor.Render("x")),
		"cursor on a suppressed row must render in the cursor style")
}

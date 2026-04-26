// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
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

	p := newPage(t)
	alerts := []backend.Alert{
		mkAlert("A", "critical", backend.AlertStateActive),
		mkAlert("B", "warning", backend.AlertStateSuppressed),
		mkAlert("C", "info", backend.AlertStateUnprocessed),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	require.Len(t, p.view, 3)

	_, _ = p.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Equal(t, string(backend.AlertStateActive), p.stateFilter)
	require.Len(t, p.view, 1)
	require.Equal(t, "A", p.view[0].a.Labels["alertname"])

	_, _ = p.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Equal(t, string(backend.AlertStateSuppressed), p.stateFilter)
	require.Len(t, p.view, 1)

	_, _ = p.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Equal(t, string(backend.AlertStateUnprocessed), p.stateFilter)

	_, _ = p.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Empty(t, p.stateFilter, "fourth `t` cycles back to all")
	require.Len(t, p.view, 3)
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

func TestPage_SilenceKeyFlashesPlaceholder(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "silence form")
	require.Equal(t, footer.FlashWarn, msg.Level,
		"placeholder must Warn so users know the affordance is unfinished, not done")
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
	// change), but the cursor row MUST carry an ANSI escape.
	lines := strings.Split(stripStyle(out), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	cursorLine := strings.SplitN(out, "\n", 4)[1] // header → row 0 (cursor) → ...
	otherLine := strings.SplitN(out, "\n", 4)[2]
	require.Contains(t, cursorLine, "\x1b[",
		"the cursor row must carry ANSI styling")
	require.NotContains(t, otherLine, "\x1b[",
		"non-cursor rows in v0.1 stay unstyled — only Cursor wraps the row")
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

	// Default scope (empty) reads as "all" — k9s convention.
	p := newPage(t)
	require.Equal(t, "alerts(all)[0]", p.Title())

	// Explicit scope from Options threads into the title.
	p2 := New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
		Scope:  "prod",
	})
	require.Equal(t, "alerts(prod)[0]", p2.Title())

	// SetScope updates the active label live.
	p2.SetScope("prod,staging")
	require.Equal(t, "alerts(prod,staging)[0]", p2.Title())
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

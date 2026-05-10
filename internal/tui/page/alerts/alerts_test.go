// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/header"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// fixedNow returns a deterministic clock for the age column tests.
var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

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
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
}

func TestPage_SeverityCellWearsThemeColour(t *testing.T) {
	t.Parallel()

	styles := testutil.LoadStyles(t)
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

	styles := testutil.LoadStyles(t)
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

	styles := testutil.LoadStyles(t)
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

	styles := testutil.LoadStyles(t)
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

	out := testutil.StripStyle(p.View(120, 20))
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

func TestPage_FullPageMotionsMoveCursor(t *testing.T) {
	t.Parallel()

	// Build enough rows that the cold-start fallback (20) lands
	// inside the table without clamping. The viewport-aware path
	// is exercised by TestPage_ViewportAwareScrollSteps.
	p := newPage(t)
	alerts := make([]backend.Alert, 60)
	for i := range alerts {
		name := fmt.Sprintf("A%02d", i)
		alerts[i] = mkAlert(name, "info", backend.AlertStateActive)
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Cold-start (no View call yet) — the page falls back to 20 so a
	// keystroke arriving before the first render still moves a
	// reasonable distance.
	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "cold-start Ctrl+F falls back to 20 rows")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+B mirrors Ctrl+F")

	// Clamps at the edges — Ctrl+F at the bottom stays put.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, 59, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 59, p.cursor, "Ctrl+F at the last row clamps; never overshoots")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()

	// After a render the page has snapshotted its body-row budget
	// (height - 1 to account for the column-header line). Ctrl+D
	// must walk half that, Ctrl+F a full window minus two — vim's
	// 'scroll' default and the standard CTRL-F overlap.
	p := newPage(t)
	alerts := make([]backend.Alert, 100)
	for i := range alerts {
		alerts[i] = mkAlert(fmt.Sprintf("A%02d", i), "info", backend.AlertStateActive)
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	_ = p.View(120, 41) // 41 - 1 header line = 40-row body

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.cursor, "Ctrl+F walks body-2 from the new cursor (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+B mirrors Ctrl+F symmetrically")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+U mirrors Ctrl+D symmetrically")
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

func TestPage_BindingsExposeSortShortcutsForHelpOverlay(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	want := map[string]string{
		"Shift+S": "sort by severity",
		"Shift+N": "sort by alertname",
		"Shift+T": "sort by state",
		"Shift+A": "sort by age",
	}
	got := map[string]string{}
	for _, b := range p.Bindings() {
		if strings.HasPrefix(b.Key, "Shift+") {
			got[b.Key] = b.Description
		}
	}
	for k, desc := range want {
		require.Contains(t, got, k,
			"Bindings() must surface %s so the `?` overlay's HOTKEYS column lists it", k)
		require.Contains(t, got[k], "sort",
			"sort entry %s must read as a sort action in the help overlay", k)
		require.Equal(t, desc, got[k],
			"sort description for %s must match the keybindings.md table", k)
	}
}

func TestPage_HeaderRendersForegroundOnly(t *testing.T) {
	t.Parallel()
	// TUI chrome stays on terminal default background — painting
	// palette bg inside the unstyled body frame creates a coloured
	// stripe. Asserts the header line carries no SGR background
	// code, preventing the regression where Table.Header / Table.
	// HeaderActive were rendered via .Render (which carries bg).
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("HighCPU", "critical", backend.AlertStateActive),
	}})
	headerLine, _, _ := strings.Cut(p.View(120, 10), "\n")
	require.NotContains(t, headerLine, "\x1b[48",
		"header must not paint a palette background — chrome stays on terminal default bg")
}

func TestPage_SortColumnWalk(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, sortKeyAge, p.sorter.ActiveKey(), "left walk wraps to the rightmost column")
}

func TestPage_SilenceKeyOnEmptyViewFlashesHint(t *testing.T) {
	t.Parallel()

	// Clients set so the "no writeable backend" path doesn't win;
	// no DataMsg → empty view.
	p := New(Options{
		Styles:  testutil.LoadStyles(t),
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
		Styles:  testutil.LoadStyles(t),
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
// needs. The single-row tests only assert `s` produces a non-
// flash Cmd; CreateSilence is never called from within the page
// in those flows. The bulk-silence tests do call CreateSilence
// from the page's fanout, so the implementation is concurrent-
// safe and records every spec seen for assertion.
type fakeSilenceClient struct {
	mu       sync.Mutex
	tenant   string
	calls    []backend.SilenceSpec
	failOn   map[string]bool // matcher.Value (alertname) → return error
	wantErr  error           // unconditional error if non-nil
	delay    time.Duration   // optional sleep before returning (concurrency tests)
	released chan struct{}   // optional gate; nil = no gating
	inflight int
	peakIn   int
}

func (f *fakeSilenceClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	f.inflight++
	if f.inflight > f.peakIn {
		f.peakIn = f.inflight
	}
	gate := f.released
	failOn := f.failOn
	wantErr := f.wantErr
	delay := f.delay
	f.mu.Unlock()

	if gate != nil {
		<-gate
	}
	if delay > 0 {
		time.Sleep(delay)
	}

	f.mu.Lock()
	f.inflight--
	f.mu.Unlock()

	if wantErr != nil {
		return "", wantErr
	}
	for _, m := range spec.Matchers {
		if m.Name == "alertname" && failOn[m.Value] {
			return "", fmt.Errorf("seeded failure for %s", m.Value)
		}
	}
	return "fake-silence-id", nil
}

func (*fakeSilenceClient) UpdateSilence(_ context.Context, _ string, _ backend.SilenceSpec) error {
	return nil
}

func (f *fakeSilenceClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSilenceClient) callsCopy() []backend.SilenceSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]backend.SilenceSpec, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeSilenceClient) peak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peakIn
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

func TestPage_UserResortKeepsCursorAtIndex(t *testing.T) {
	t.Parallel()

	// User-initiated re-sort is k9s-positional: the cursor stays at
	// the same row index, whatever alert lands under it becomes the
	// new focus. This pairs with TestPage_CursorPreservedAcrossDataRefresh:
	// poll refreshes follow the alert content; sort keystrokes follow
	// the row position.
	p := newPage(t)
	withFP := func(name, severity, fp string) backend.Alert {
		a := mkAlert(name, severity, backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		withFP("A", "info", "fp-a"),
		withFP("B", "critical", "fp-b"),
		withFP("C", "warning", "fp-c"),
	}})
	// Default sort is severity DESC: critical, warning, info →
	// fp-b, fp-c, fp-a. Move cursor to row 1 (fp-c).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.cursor)
	require.Equal(t, "fp-c", p.view[p.cursor].a.Fingerprint)

	// Re-sort by alertname ASC → A, B, C → fp-a, fp-b, fp-c.
	// Cursor must stay at row index 1 (fp-b), NOT follow fp-c
	// to row 2.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.Equal(t, 1, p.cursor, "cursor must stay at row index on user re-sort")
	require.Equal(t, "fp-b", p.view[p.cursor].a.Fingerprint,
		"the alert under the cursor at the new index becomes the new focus")

	// A subsequent poll refresh must now track fp-b (the new focus
	// captured after the re-sort), confirming snapshotFocus re-armed
	// the find-by-fingerprint path for non-sort recomputes.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		withFP("Z", "info", "fp-z"),
		withFP("A", "info", "fp-a"),
		withFP("B", "critical", "fp-b"),
	}})
	// New sort order (by alertname ASC): A, B, Z → fp-a, fp-b, fp-z.
	// Cursor follows fp-b to row index 1.
	require.Equal(t, "fp-b", p.view[p.cursor].a.Fingerprint,
		"after a sort, subsequent poll refreshes must follow the new focus")
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
	require.Contains(t, testutil.StripStyle(row0), "✓",
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

	out := testutil.StripStyle(p.View(120, 20))
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

// TestPage_HandleMotionUpdatesTopRowWithoutRender pins the contract
// that cursor mutations proactively reconcile topRow, so handlers
// don't depend on a subsequent View call to settle scroll state.
// Failing this test means the page's Update→View ordering is
// load-bearing: a headless test of motion + read-state would need
// an out-of-band View() call to be correct.
func TestPage_HandleMotionUpdatesTopRowWithoutRender(t *testing.T) {
	t.Parallel()

	alerts := make([]backend.Alert, 30)
	for i := range alerts {
		alerts[i] = mkAlert(fmt.Sprintf("Alert%02d", i), "warning", backend.AlertStateActive)
		alerts[i].Fingerprint = fmt.Sprintf("fp-%02d", i)
	}
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Seed bodyHeight as if a render had already established the
	// viewport budget — handlers read this cache to compute scroll.
	p.bodyHeight = 5

	// Walk past the seeded viewport.
	for range 20 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}

	require.Positive(t, p.topRow,
		"handleMotion must reconcile topRow without a subsequent View() call")
	require.GreaterOrEqual(t, p.cursor, p.topRow,
		"cursor must remain on or after the visible window's first row")
	require.Less(t, p.cursor, p.topRow+p.bodyHeight,
		"cursor must remain inside the visible window")
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
	out := testutil.StripStyle(p.View(80, 10))
	require.Contains(t, out, p.view[p.cursor].a.Labels["alertname"],
		"viewport must scroll so the cursor row stays visible")

	// G jumps to the last row; the bottom of the list must be in
	// view (the page scrolled all the way down).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	out = testutil.StripStyle(p.View(80, 10))
	require.Contains(t, out, "Alert29",
		"G must scroll the viewport to the last row")

	// gg-equivalent: cursor back to 0 → top-of-list visible again.
	for range 30 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	out = testutil.StripStyle(p.View(80, 10))
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
	lines := strings.Split(testutil.StripStyle(out), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	cursorLine := strings.SplitN(out, "\n", 4)[1] // header → row 0 (cursor) → ...
	otherLine := strings.SplitN(out, "\n", 4)[2]
	require.Contains(t, cursorLine, "\x1b[",
		"the cursor row must carry ANSI styling")
	// Non-cursor rows do carry per-cell severity ANSI now. Assert
	// the Cursor style ANSI is absent — that's the contract that
	// keeps the cursor visually distinct from a coloured cell.
	styles := testutil.LoadStyles(t)
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
	require.Equal(t, sortKeyName, p.sorter.ActiveKey(),
		"Shift+N must sort by alertname directly (no walk)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'A', Text: "A", Mod: tea.ModShift})
	require.Equal(t, sortKeyAge, p.sorter.ActiveKey())
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S", Mod: tea.ModShift})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
}

func TestPage_SortShortcutTogglesDirection(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// Severity starts active, descending (critical first).
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc())

	// Pressing the active column's shortcut flips the direction.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc(), "second Shift+S must flip to ascending")

	// A third press flips back to descending.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.False(t, p.sorter.Asc(), "third Shift+S must flip back to descending")

	// Switching to a different column resets to that column's
	// default direction (ascending for non-severity columns).
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc(), "switching column resets to default direction")

	// Pressing the new column's shortcut flips it.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N"})
	require.False(t, p.sorter.Asc(), "second Shift+N must flip alertname to descending")
}

func TestPage_HLWalkResetsDirection(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	// Flip severity to ascending first.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	require.True(t, p.sorter.Asc())

	// l walks to the next column (Name) — must reset direction
	// to the new column's default (ascending), regardless of
	// what the previous column's direction was.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	require.Equal(t, sortKeyName, p.sorter.ActiveKey())
	require.True(t, p.sorter.Asc())

	// h walks back. Severity's default is descending.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	require.Equal(t, sortKeySeverity, p.sorter.ActiveKey())
	require.False(t, p.sorter.Asc(),
		"walking back to severity must reset to its default (descending)")
}

func TestPage_TenantColumnAppearsForAllScope(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles: testutil.LoadStyles(t),
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

	out := testutil.StripStyle(p.View(140, 20))
	require.Contains(t, out, "TENANT")
	require.Contains(t, out, "prod")
	require.Contains(t, out, "staging")
}

func TestPage_TenantColumnHiddenForSingleBackend(t *testing.T) {
	t.Parallel()

	p := New(Options{
		Styles: testutil.LoadStyles(t),
		Now:    func() time.Time { return fixedNow },
		Scope:  "prod",
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("A", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})

	out := testutil.StripStyle(p.View(140, 20))
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
		Styles: testutil.LoadStyles(t),
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
	out := testutil.StripStyle(p.Title())
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
	out := testutil.StripStyle(p.View(140, 20))
	require.Contains(t, out, "1m ago")
	require.NotContains(t, out, "2026-",
		"relative mode must not surface the absolute date")

	// Flip to absolute — body shows the ISO local stamp. Per
	// post-batch UX call (max real-estate), the time mode is NOT
	// surfaced in HeaderContent; the toggle's flash is the
	// affordance signal and the cell content speaks for itself.
	_, _ = p.Update(app.TimeFormatChangedMsg{Format: app.TimeFormatAbsolute})
	out = testutil.StripStyle(p.View(140, 20))
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
		Styles: testutil.LoadStyles(t),
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
	out := testutil.StripStyle(p.View(140, 20))
	require.Contains(t, out, "A", "all-scope view shows both alerts")
	require.Contains(t, out, "B")
	require.Contains(t, out, "TENANT",
		"two tenants in scope keeps the TENANT column visible")

	// `<1>` quick-switch arrives via the bubbletea bus.
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "prod"})
	require.Equal(t, "alerts(prod)[1]", p.Title(),
		"scope change must rescope the [N] count, not just the label")
	out = testutil.StripStyle(p.View(140, 20))
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
	out = testutil.StripStyle(p.View(140, 20))
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

func TestPage_ReadOnlyDropsSilenceBinding(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:   testutil.LoadStyles(t),
		Now:      func() time.Time { return fixedNow },
		ReadOnly: true,
	})
	for _, a := range p.Bindings() {
		require.NotEqual(t, "s", a.Key,
			"read-only Bindings() must drop the `s` silence verb so the hint strip / help overlay reflect the read-only mode")
	}
}

func TestPage_ReadOnlySilenceKeyFlashesHint(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:   testutil.LoadStyles(t),
		Now:      func() time.Time { return fixedNow },
		ReadOnly: true,
		Clients:  map[string]silenceform.Client{"prod": &fakeSilenceClient{}},
	})
	_, _ = p.Update(poll.DataMsg{
		Tenant:   "prod",
		Resource: []backend.Alert{mkAlert("X", "warning", backend.AlertStateActive)},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd()
	fm, ok := msg.(footer.FlashShowMsg)
	require.True(t, ok, "expected a footer.FlashShowMsg, got %T", msg)
	require.Equal(t, footer.FlashWarn, fm.Level)
	require.Contains(t, fm.Text, "read-only")
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
	out := testutil.StripStyle(p.View(80, 5))
	require.Contains(t, out, "no alerts (yet)",
		"with no data and no filter the empty state explains polling")

	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "nope"})
	out = testutil.StripStyle(p.View(80, 5))
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
// testutil.StripStyle path erases SGR sequences, so the assertion
// compares against a freshly-rendered probe through the same
// Dimmed style: if the row contains the probe's SGR prefix, the
// dimmed branch fired.
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
	require.Contains(t, testutil.StripStyle(out), "Silenced",
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
	// sortKeySeverity desc: critical at row 0, warning at row 1.
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
	// Cursor beats both marked and dimmed. Per the k9s parity
	// rework, the cursor row's bg tracks the row's severity colour
	// — so the expected SGR is Cursor with bg overridden to the
	// row's severity.warning fg, not the static cursorBgColor.
	p := newPage(t)
	suppressed := mkAlert("Silenced", "warning", backend.AlertStateSuppressed)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{suppressed}})
	// Cursor is on row 0 by default.

	out := p.View(120, 10)
	wantStyle := p.styles.Table.CursorOver(p.styles.Severity.Warning.GetForeground())
	require.Contains(t, out, stylePrefix(t, wantStyle.Render("x")),
		"cursor on a suppressed row must render in the cursor-over-severity style")
}

// alertWithFP builds a synthetic Alert with a stable fingerprint
// + tenant-aware labels for the bulk-silence tests. Fingerprint
// is the bulk-silence mark key; alertname is the matcher value
// failOn checks for in the fake.
func alertWithFP(name, fp, severity string) backend.Alert {
	return backend.Alert{
		Fingerprint: fp,
		Labels: map[string]string{
			"alertname": name,
			"severity":  severity,
		},
		State:    backend.AlertStateActive,
		StartsAt: fixedNow.Add(-time.Minute),
	}
}

// bulkPage builds a page with a populated client map for one or
// more tenants and seeds it with an alert payload per tenant.
// alertsByTenant keys the Tenant tag of each DataMsg so bulk
// fanout tests can drive multi-tenant cases without a custom
// scaffold per test. Tenants are walked in alphabetical order so
// the resulting cursor / focus state is deterministic across runs
// (Go's map iteration order is randomized).
func bulkPage(t *testing.T, alertsByTenant map[string][]backend.Alert, fakes map[string]*fakeSilenceClient, concurrency int) *Page {
	t.Helper()
	clients := map[string]silenceform.Client{}
	for tenant, fake := range fakes {
		fake.tenant = tenant
		clients[tenant] = fake
	}
	p := New(Options{
		Styles:          testutil.LoadStyles(t),
		Now:             func() time.Time { return fixedNow },
		Scope:           "all",
		Clients:         clients,
		Creator:         "wilfried",
		BulkConcurrency: concurrency,
	})
	tenants := make([]string, 0, len(alertsByTenant))
	for tenant := range alertsByTenant {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	for _, tenant := range tenants {
		_, _ = p.Update(poll.DataMsg{Resource: alertsByTenant[tenant], Tenant: tenant})
	}
	return p
}

// markEvery walks the cursor down through the view marking every
// row with Space. Resets the cursor to row 0 first via
// GoToFirstRowMsg so the marks are applied to the visible rows
// in order regardless of where DataMsg-arrival ordering left the
// cursor. Stops at the last row so the cursor remains in bounds
// for any subsequent assertions.
func markEvery(p *Page) {
	_, _ = p.Update(app.GoToFirstRowMsg{})
	for range len(p.view) {
		_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
}

// runBulkSilence drives a posted bulk-silence form submission
// through dispatch + done so the page state settles.
func runBulkSilence(t *testing.T, p *Page, msg silenceform.BulkSubmittedMsg) tea.Cmd {
	t.Helper()
	_, dispatch := p.Update(msg)
	require.NotNil(t, dispatch, "BulkSubmittedMsg must produce a dispatch Cmd")
	doneMsg := dispatch()
	done, ok := doneMsg.(bulkSilenceDoneMsg)
	require.True(t, ok, "dispatch must emit bulkSilenceDoneMsg, got %T", doneMsg)
	_, flashCmd := p.Update(done)
	return flashCmd
}

func TestPage_SKeyNoMarksUsesCursor(t *testing.T) {
	t.Parallel()

	// No marks → existing single-row form push. Pending state
	// stays empty since the bulk path never engages.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {alertWithFP("HighCPU", "fp-a", "critical")},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "no marks → s must push the single-row silence form, not flash")
	require.Empty(t, p.pendingBulkSilence.targets, "no-marks path must not populate the bulk pending state")
}

func TestPage_SKeyOneMarkPushesBulkFormDirectly(t *testing.T) {
	t.Parallel()

	// One mark → bulk form pushed directly, no confirm modal.
	// pendingBulkSilence holds the single resolved target.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {alertWithFP("HighCPU", "fp-a", "critical")},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 1)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "1-mark path must push the bulk form, not flash")
	require.Len(t, p.pendingBulkSilence.targets, 1)
	require.Equal(t, "fp-a", p.pendingBulkSilence.targets[0].Fingerprint)
}

func TestPage_SKeyTwoMarksOpensConfirmFirst(t *testing.T) {
	t.Parallel()

	// Two marks → confirm modal opens with the bulk question; the
	// form is pushed only after Yes.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {
			alertWithFP("HighCPU", "fp-a", "critical"),
			alertWithFP("DiskFull", "fp-b", "warning"),
		},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 2)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd, "2-mark path must open confirm modal")
	require.Len(t, p.pendingBulkSilence.targets, 2)
}

func TestPage_BulkSilencePerAlertMatchers(t *testing.T) {
	t.Parallel()

	// Three marked alerts with distinct labels → 3 CreateSilence
	// calls, each with that alert's labels (minus __name__). The
	// matchers must round-trip through MatchersFromLabels so the
	// synthetic key is dropped.
	fake := &fakeSilenceClient{}
	alerts := []backend.Alert{
		alertWithFP("A", "fp-a", "critical"),
		alertWithFP("B", "fp-b", "warning"),
		alertWithFP("C", "fp-c", "info"),
	}
	// Inject __name__ on each so we can prove it's dropped.
	for i := range alerts {
		alerts[i].Labels["__name__"] = "ALERTS"
	}
	p := bulkPage(t, map[string][]backend.Alert{"prod": alerts},
		map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	require.Len(t, p.marks, 3)

	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	_ = runBulkSilence(t, p, silenceform.BulkSubmittedMsg{
		Comment:  "ack",
		Creator:  "wilfried",
		StartsAt: fixedNow,
		EndsAt:   fixedNow.Add(time.Hour),
	})

	require.Equal(t, 3, fake.callCount(), "one CreateSilence per marked alert")
	for _, call := range fake.callsCopy() {
		for _, m := range call.Matchers {
			require.NotEqual(t, "__name__", m.Name,
				"__name__ must be dropped by MatchersFromLabels")
		}
		require.NotEmpty(t, call.Matchers, "every call must carry per-alert matchers")
		require.Equal(t, "ack", call.Comment)
		require.Equal(t, "wilfried", call.CreatedBy)
	}
}

func TestPage_BulkSilencePerTenantDispatch(t *testing.T) {
	t.Parallel()

	// 2 alerts on tenant A, 1 on tenant B → A's client sees 2
	// calls, B's sees 1, no cross-tenant leakage.
	fakeA := &fakeSilenceClient{}
	fakeB := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod":    {alertWithFP("A1", "fp-a1", "warning"), alertWithFP("A2", "fp-a2", "warning")},
		"staging": {alertWithFP("B1", "fp-b1", "info")},
	}, map[string]*fakeSilenceClient{"prod": fakeA, "staging": fakeB}, 4)
	require.Len(t, p.view, 3)
	markEvery(p)
	require.Len(t, p.marks, 3)

	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	_ = runBulkSilence(t, p, silenceform.BulkSubmittedMsg{
		Comment:  "ack",
		Creator:  "wilfried",
		StartsAt: fixedNow,
		EndsAt:   fixedNow.Add(time.Hour),
	})

	require.Equal(t, 2, fakeA.callCount(), "tenant prod gets 2 calls")
	require.Equal(t, 1, fakeB.callCount(), "tenant staging gets 1 call")
}

func TestPage_BulkSilenceUnmarksOnlySuccessfulFingerprints(t *testing.T) {
	t.Parallel()

	// fp-a and fp-c succeed; fp-b fails. After fanout completes,
	// only fp-b remains marked so the next `s` retries that one.
	fake := &fakeSilenceClient{failOn: map[string]bool{"B": true}}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {
			alertWithFP("A", "fp-a", "warning"),
			alertWithFP("B", "fp-b", "warning"),
			alertWithFP("C", "fp-c", "warning"),
		},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	flashCmd := runBulkSilence(t, p, silenceform.BulkSubmittedMsg{
		Comment: "ack", Creator: "wilfried",
		StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour),
	})
	flash := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, flash.Level)
	require.Contains(t, flash.Text, "silenced 2 of 3 — 1 failed")

	require.Len(t, p.marks, 1, "only the failed fingerprint keeps its mark")
	require.Contains(t, p.marks, "fp-b")
}

func TestPage_BulkSilenceFlashesAllSuccess(t *testing.T) {
	t.Parallel()

	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {
			alertWithFP("A", "fp-a", "warning"),
			alertWithFP("B", "fp-b", "warning"),
		},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	flashCmd := runBulkSilence(t, p, silenceform.BulkSubmittedMsg{
		Comment: "ack", Creator: "wilfried",
		StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour),
	})
	flash := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, flash.Level)
	require.Contains(t, flash.Text, "silenced 2 alerts")
	require.Empty(t, p.marks, "every successful target drops its mark")
}

func TestPage_BulkSilenceFlashesTotalFailure(t *testing.T) {
	t.Parallel()

	fake := &fakeSilenceClient{wantErr: errors.New("boom")}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {
			alertWithFP("A", "fp-a", "warning"),
			alertWithFP("B", "fp-b", "warning"),
		},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	flashCmd := runBulkSilence(t, p, silenceform.BulkSubmittedMsg{
		Comment: "ack", Creator: "wilfried",
		StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour),
	})
	flash := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, flash.Level)
	require.Contains(t, flash.Text, "silence failed for 2 alerts")
	require.Len(t, p.marks, 2, "every failed target keeps its mark")
}

func TestPage_BulkSilenceConfirmNoIsNoop(t *testing.T) {
	t.Parallel()

	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {
			alertWithFP("A", "fp-a", "warning"),
			alertWithFP("B", "fp-b", "warning"),
		},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: false})

	require.Nil(t, cmd, "No on the bulk-silence confirm must be a noop")
	require.Equal(t, 0, fake.callCount())
	require.Empty(t, p.pendingBulkSilence.targets, "pending state must clear on No")
	require.Len(t, p.marks, 2, "marks survive a cancelled confirm so the user can retry")
}

func TestPage_BulkSilenceRespectsConcurrency(t *testing.T) {
	t.Parallel()

	// concurrency=2 with 5 marked alerts on one tenant → at most
	// 2 callers in flight at any moment. Use a gating channel so
	// the test can hold callers blocked, observe inflight, and
	// release in a controlled order.
	gate := make(chan struct{}, 256)
	fake := &fakeSilenceClient{released: gate}
	alerts := make([]backend.Alert, 0, 5)
	for i := range 5 {
		alerts = append(alerts, alertWithFP(string(rune('A'+i)), fmt.Sprintf("fp-%d", i), "warning"))
	}
	p := bulkPage(t, map[string][]backend.Alert{"prod": alerts},
		map[string]*fakeSilenceClient{"prod": fake}, 2)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	_, dispatch := p.Update(silenceform.BulkSubmittedMsg{
		Comment: "ack", Creator: "wilfried",
		StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour),
	})
	resultCh := make(chan tea.Msg, 1)
	go func() { resultCh <- dispatch() }()

	require.Eventually(t, func() bool { return fake.peak() >= 2 }, time.Second, time.Millisecond)
	for range 5 {
		gate <- struct{}{}
	}
	<-resultCh
	require.LessOrEqual(t, fake.peak(), 2,
		"concurrency=2 must cap in-flight callers per tenant; peak=%d", fake.peak())
}

func TestPage_BulkSilenceCancelsOnPageClose(t *testing.T) {
	t.Parallel()

	// concurrency=1 with 5 marks → release the first call, Close
	// the page mid-flight, observe the producer goroutine sees
	// ctx.Done and stops feeding work. No further callers should
	// reach the fake after Close.
	gate := make(chan struct{}, 256)
	fake := &fakeSilenceClient{released: gate}
	alerts := make([]backend.Alert, 0, 5)
	for i := range 5 {
		alerts = append(alerts, alertWithFP(string(rune('A'+i)), fmt.Sprintf("fp-%d", i), "warning"))
	}
	p := bulkPage(t, map[string][]backend.Alert{"prod": alerts},
		map[string]*fakeSilenceClient{"prod": fake}, 1)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})

	_, dispatch := p.Update(silenceform.BulkSubmittedMsg{
		Comment: "ack", Creator: "wilfried",
		StartsAt: fixedNow, EndsAt: fixedNow.Add(time.Hour),
	})
	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- dispatch() }()

	require.Eventually(t, func() bool { return fake.callCount() >= 1 }, time.Second, time.Millisecond)
	_ = p.Close()
	gate <- struct{}{} // release the in-flight caller so the round can drain
	doneMsg := <-doneCh
	done := doneMsg.(bulkSilenceDoneMsg)
	require.Less(t, len(done.successes), 5,
		"Close must short-circuit the fanout; got %d successes", len(done.successes))
	require.LessOrEqual(t, fake.callCount(), 1,
		"after Close the dispatcher must not start additional CreateSilence calls; got %d", fake.callCount())
}

func TestPage_ClearMarksMsgEmptiesMarks(t *testing.T) {
	t.Parallel()

	// Ctrl+\ at LayerGlobal lands as ClearMarksMsg. With one or
	// more marks active, the page empties the map and flashes
	// "marks cleared" so the user sees confirmation.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {
			alertWithFP("A", "fp-a", "warning"),
			alertWithFP("B", "fp-b", "warning"),
		},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	require.Len(t, p.marks, 2)

	_, cmd := p.Update(app.ClearMarksMsg{})
	require.Empty(t, p.marks, "ClearMarksMsg must drop every mark")
	require.NotNil(t, cmd, "non-empty pre-clear count must surface a flash")
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "marks cleared")
}

func TestPage_ClearMarksMsgWithNoMarksIsSilent(t *testing.T) {
	t.Parallel()

	// ClearMarksMsg arriving while no marks are active must be a
	// silent no-op — flashing "marks cleared" on every Ctrl+\ press
	// would be surprising spam on pages without any marked rows.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {alertWithFP("A", "fp-a", "warning")},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	require.Empty(t, p.marks)

	_, cmd := p.Update(app.ClearMarksMsg{})
	require.Empty(t, p.marks)
	require.Nil(t, cmd, "no-marks ClearMarksMsg must not flash")
}

func TestPage_BulkSilenceEmptyAfterResolve(t *testing.T) {
	t.Parallel()

	// Mark a fingerprint, then a poll tick drops it from byTenant
	// (alert resolved server-side between mark and silence). Press
	// `s` — resolveBulkSilenceTargets returns an empty list and the
	// page must flash the soft-info "no marked alerts remain" hint
	// rather than open the bulk form against a stale snapshot.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {alertWithFP("HighCPU", "fp-a", "warning")},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 1)

	// Poll tick: alert resolved, byTenant empties.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{}, Tenant: "prod"})
	require.Empty(t, p.view)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "no marked alerts remain")
	require.Empty(t, p.pendingBulkSilence.targets,
		"empty-resolve must not leak pending state into the next round")
}

func TestPage_BulkSilenceCancelledMsgDropsPending(t *testing.T) {
	t.Parallel()

	// Pushing the bulk form then pressing Esc on it must drop the
	// pending target list so a subsequent `s` doesn't accidentally
	// reuse a stale snapshot of marks.
	fake := &fakeSilenceClient{}
	p := bulkPage(t, map[string][]backend.Alert{
		"prod": {alertWithFP("A", "fp-a", "warning"), alertWithFP("B", "fp-b", "warning")},
	}, map[string]*fakeSilenceClient{"prod": fake}, 4)
	markEvery(p)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	_, _ = p.Update(modal.ConfirmResultMsg{Yes: true})
	require.Len(t, p.pendingBulkSilence.targets, 2)

	_, _ = p.Update(silenceform.CancelledMsg{})
	require.Empty(t, p.pendingBulkSilence.targets)
}

func TestPage_FormatTenantBreakdownAlerts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []bulkSilenceTarget
		want string
	}{
		{
			name: "single tenant",
			in:   []bulkSilenceTarget{{Tenant: "prod", Fingerprint: "fp-a"}, {Tenant: "prod", Fingerprint: "fp-b"}},
			want: "prod",
		},
		{
			name: "multi tenant sorted",
			in: []bulkSilenceTarget{
				{Tenant: "staging", Fingerprint: "fp-x"},
				{Tenant: "prod", Fingerprint: "fp-a"},
				{Tenant: "prod", Fingerprint: "fp-b"},
			},
			want: "prod=2, staging=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, formatTenantBreakdownAlerts(tc.in))
		})
	}
}

func TestPage_BulkSilenceBanner(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		targets []bulkSilenceTarget
		tenants []string
		want    string
	}{
		{
			name:    "single tenant single alert",
			targets: []bulkSilenceTarget{{Tenant: "prod", Fingerprint: "fp-a"}},
			tenants: []string{"prod"},
			want:    "applies to 1 alert (tenant prod)",
		},
		{
			name:    "single tenant multiple alerts",
			targets: []bulkSilenceTarget{{Tenant: "prod"}, {Tenant: "prod"}, {Tenant: "prod"}},
			tenants: []string{"prod"},
			want:    "applies to 3 alerts (tenant prod)",
		},
		{
			name:    "multi tenant",
			targets: []bulkSilenceTarget{{Tenant: "prod"}, {Tenant: "prod"}, {Tenant: "staging"}},
			tenants: []string{"prod", "staging"},
			want:    "applies to 3 alerts across 2 tenants — each silenced with its own labels",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, bulkSilenceBanner(tc.targets, tc.tenants))
		})
	}
}

func TestPage_WatchModeToggleSwallowsDataMsg(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	// First snapshot lands normally.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("first", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})
	require.NotEmpty(t, p.byTenant["prod"], "first DataMsg must populate byTenant")

	// `w` pauses watch.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.True(t, p.paused, "w must toggle paused on")

	// Subsequent DataMsg is swallowed: byTenant stays at the old snapshot.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{
			mkAlert("first", "warning", backend.AlertStateActive),
			mkAlert("second", "critical", backend.AlertStateActive),
		},
		Tenant: "prod",
	})
	require.Len(t, p.byTenant["prod"], 1,
		"paused page must drop incoming DataMsg")

	// `w` again resumes; the next DataMsg lands.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.False(t, p.paused)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{
			mkAlert("first", "warning", backend.AlertStateActive),
			mkAlert("second", "critical", backend.AlertStateActive),
			mkAlert("third", "info", backend.AlertStateActive),
		},
		Tenant: "prod",
	})
	require.Len(t, p.byTenant["prod"], 3, "resumed page accepts the next DataMsg")
}

func TestPage_WatchModeManualRefreshHonouredOnce(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("first", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})

	// Pause watch.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.True(t, p.paused)

	// Manual `r` press — sets pausedRefresh, returns a Cmd that
	// emits a RefreshRequestedMsg. The next DataMsg is honoured
	// (the operator deliberately pulled it).
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.NotNil(t, cmd)
	require.True(t, p.pausedRefresh, "r press while paused must set pausedRefresh")

	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{
			mkAlert("first", "warning", backend.AlertStateActive),
			mkAlert("second", "critical", backend.AlertStateActive),
		},
		Tenant: "prod",
	})
	require.Len(t, p.byTenant["prod"], 2,
		"r press while paused must pass through the next DataMsg")
	require.False(t, p.pausedRefresh, "pausedRefresh must clear after one tick")
	require.True(t, p.paused, "manual refresh does NOT exit paused state")

	// Subsequent ordinary tick is dropped again (paused, no
	// pending refresh).
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{},
		Tenant:   "prod",
	})
	require.Len(t, p.byTenant["prod"], 2, "subsequent ticks resume being dropped")
}

func TestPage_WatchModeFooterRendersWatchOff(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Alert{mkAlert("first", "warning", backend.AlertStateActive)},
		Tenant:   "prod",
	})
	require.NotContains(t, p.Footer(), "WATCH OFF",
		"baseline footer omits WATCH OFF")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	require.Contains(t, p.Footer(), "WATCH OFF",
		"paused page footer leads with WATCH OFF")
}

func TestPage_ErrorBandReflectsBackendStatusDetail(t *testing.T) {
	t.Parallel()
	p := newPage(t)

	// No errors → empty band.
	require.Empty(t, p.ErrorBand())

	// Failure transition with a Detail string surfaces it.
	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "connection refused",
	})
	require.Equal(t, "connection refused", p.ErrorBand(),
		"single-tenant scope renders detail verbatim (no tenant prefix)")

	// Recovery transition (Detail empty) clears the row.
	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnConnected,
		Detail: "",
	})
	require.Empty(t, p.ErrorBand(),
		"recovery clears the band so transient blips don't linger")
}

func TestPage_ErrorBandPrefixesTenantOnAllScope(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	p.scope = "all"

	_, _ = p.Update(poll.BackendStatusMsg{
		Tenant: "prod",
		State:  header.ConnUnreachable,
		Detail: "401 unauthorised",
	})
	require.Equal(t, "prod: 401 unauthorised", p.ErrorBand(),
		"all-scope view prefixes tenant so the operator knows which one")
}

func TestPage_ErrorBandCollapsesMultipleOffenders(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	p.scope = "all"

	_, _ = p.Update(poll.BackendStatusMsg{Tenant: "alpha", State: header.ConnUnreachable, Detail: "down"})
	_, _ = p.Update(poll.BackendStatusMsg{Tenant: "beta", State: header.ConnUnreachable, Detail: "401"})

	// Sorted-by-tenant: alpha is the first offender.
	require.Equal(t, "2 backends erroring; alpha: down", p.ErrorBand())
}

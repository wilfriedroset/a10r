// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
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
func alert(name, severity string, state backend.AlertState) backend.Alert {
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
	return New(loadStyles(t), func() time.Time { return fixedNow })
}

func TestPage_DataMsgPopulatesView(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		alert("HighCPU", "critical", backend.AlertStateActive),
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
	require.Empty(t, p.all)
}

func TestPage_SortBySeverityPutsCriticalFirst(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		alert("WarnFoo", "warning", backend.AlertStateActive),
		alert("CritBar", "critical", backend.AlertStateActive),
		alert("InfoBaz", "info", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	require.Equal(t, "CritBar", p.view[0].Labels["alertname"])
	require.Equal(t, "WarnFoo", p.view[1].Labels["alertname"])
	require.Equal(t, "InfoBaz", p.view[2].Labels["alertname"])
}

func TestPage_FilterSubstringAppliesAcrossLabels(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		alert("HighCPU", "critical", backend.AlertStateActive),
		alert("DiskSpace", "warning", backend.AlertStateActive),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})

	// Filter via the prompt-submitted contract.
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "disk"})
	require.Len(t, p.view, 1)
	require.Equal(t, "DiskSpace", p.view[0].Labels["alertname"])
}

func TestPage_FilterCancelRestoresPreFilter(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	alerts := []backend.Alert{
		alert("HighCPU", "critical", backend.AlertStateActive),
		alert("DiskSpace", "warning", backend.AlertStateActive),
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
		alert("A", "critical", backend.AlertStateActive),
		alert("B", "warning", backend.AlertStateActive),
		alert("C", "info", backend.AlertStateActive),
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
		alert("A", "critical", backend.AlertStateActive),
		alert("B", "warning", backend.AlertStateSuppressed),
		alert("C", "info", backend.AlertStateUnprocessed),
	}
	_, _ = p.Update(poll.DataMsg{Resource: alerts})
	require.Len(t, p.view, 3)

	_, _ = p.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	require.Equal(t, string(backend.AlertStateActive), p.stateFilter)
	require.Len(t, p.view, 1)
	require.Equal(t, "A", p.view[0].Labels["alertname"])

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
	mkAlert := func(name, fp string) backend.Alert {
		a := alert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	first := []backend.Alert{
		mkAlert("A", "fp-a"),
		mkAlert("B", "fp-b"),
		mkAlert("C", "fp-c"),
	}
	_, _ = p.Update(poll.DataMsg{Resource: first})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "fp-b", p.view[p.cursor].Fingerprint)

	// New tick: B has shifted to the bottom (new alerts inserted
	// above it). Cursor must follow B.
	second := []backend.Alert{
		mkAlert("X", "fp-x"),
		mkAlert("Y", "fp-y"),
		mkAlert("A", "fp-a"),
		mkAlert("B", "fp-b"),
	}
	_, _ = p.Update(poll.DataMsg{Resource: second})
	require.Equal(t, "fp-b", p.view[p.cursor].Fingerprint,
		"cursor must follow the focused alert across poll refreshes")
}

func TestPage_CursorClampsWhenFocusedAlertGone(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	mkAlert := func(name, fp string) backend.Alert {
		a := alert(name, "warning", backend.AlertStateActive)
		a.Fingerprint = fp
		return a
	}
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("A", "fp-a"),
		mkAlert("B", "fp-b"),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "fp-b", p.view[p.cursor].Fingerprint)

	// B is gone; cursor must clamp to the last remaining row.
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("A", "fp-a"),
	}})
	require.Equal(t, 0, p.cursor)
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

func TestPage_CrumbAndHeader(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, "alerts", p.Crumb())
	require.Contains(t, p.HeaderContent(), "sort:severity")
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

func TestPage_AgeFormatting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		age  time.Duration
		want string
	}{
		{name: "now", age: 100 * time.Millisecond, want: "now"},
		{name: "5s", age: 5 * time.Second, want: "5s ago"},
		{name: "2m", age: 2 * time.Minute, want: "2m ago"},
		{name: "3h", age: 3 * time.Hour, want: "3h ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, formatAge(fixedNow, fixedNow.Add(-tc.age)))
		})
	}
}

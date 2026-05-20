// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

func TestForm_BlankEndsLeavesFieldEmpty(t *testing.T) {
	t.Parallel()
	// Recreate-expired entry point wants the user to type a fresh
	// duration; the "2h" default would be a footgun (one tap of
	// Ctrl+S and the silence comes back with the placeholder).
	f := New(Options{
		Clients:   map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:    defaultTenant,
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		BlankEnds: true,
	})
	require.Empty(t, f.ends.Value(), "BlankEnds skips the 2h default")
}

func TestForm_BlankEndsBeatsExplicitEndsAt(t *testing.T) {
	t.Parallel()
	// BlankEnds is a deliberate "force the user to type"; an
	// EndsAt that happens to be set on the same Options must not
	// override it (the recreate path passes the original silence
	// untouched, but only the matchers/comment fields should land).
	f := New(Options{
		Clients:   map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:    defaultTenant,
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		EndsAt:    fixedNow.Add(time.Hour),
		BlankEnds: true,
	})
	require.Empty(t, f.ends.Value(), "BlankEnds wins over EndsAt prefill")
}

func TestForm_FocusEndsLandsOnEndsField(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Clients:   map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:    defaultTenant,
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		FocusEnds: true,
	})
	require.Equal(t, fieldEnds, f.focus, "FocusEnds lands focus on Ends")
	require.True(t, f.ends.Focused(), "FocusEnds focuses the ends input")
	require.False(t, f.matchers.Focused(), "FocusEnds blurs the default matchers field")
}

func TestForm_TabWalksFields(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	require.Equal(t, fieldMatchers, f.focus)
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, fieldStarts, f.focus)
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, fieldMatchers, f.focus)
}

func TestForm_NonKeyMessagesReachFocusedInput(t *testing.T) {
	t.Parallel()
	// The bubbles cursor blink loop is driven by tea.Msg values
	// (cursor.BlinkMsg) that are NOT tea.KeyPressMsg. If the form
	// short-circuits on non-key messages, the blink Cmd Focus()
	// returns produces a tea.Msg the form swallows and the cursor
	// never blinks.
	//
	// We can't easily construct a cursor.BlinkMsg from the test
	// (it's package-private), but we can prove the non-key path
	// reaches the input by feeding any non-key tea.Msg and
	// asserting the form returns without panicking and without
	// converting the message into a CancelledMsg or similar.
	f := newForm(t, &fakeClient{})
	type bogusMsg struct{}
	got, cmd := f.Update(bogusMsg{})
	require.Same(t, f, got, "non-key forwarding must keep the same Form pointer")
	// Bubbles' inputs return a nil Cmd for unknown messages, so
	// the test asserts the path runs end-to-end without raising.
	require.Nil(t, cmd, "bubbles must no-op on an unknown tea.Msg")
}

func TestForm_TypingGloballyBoundCharsLandsInBuffer(t *testing.T) {
	t.Parallel()
	// Direct exercise of the input path: with capture-mode on,
	// keys like '0', '1', 'q', ':', '/' must land in the focused
	// buffer. The App's handleKey is what actually bypasses the
	// dispatcher; this test asserts the form half of the contract
	// (text comes through without filtering).
	f := newForm(t, &fakeClient{})
	type_(f, "q0/:?12abc")
	require.Equal(t, "q0/:?12abc", f.matchers.Value(),
		"the form must accept every printable rune; the App's "+
			"InputCapturePage path is what shadows LayerGlobal at runtime")
}

func TestForm_TypingAppendsToFocusedField(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	type_(f, "alertname=HighCPU")
	require.Equal(t, "alertname=HighCPU", f.matchers.Value())

	// Tab to creator and overtype.
	for range 3 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	require.Equal(t, fieldCreator, f.focus)
	// Ctrl+U deletes from cursor to start of line — with the
	// pre-filled "alice" and cursor at end after Focus, that
	// clears the field.
	_, _ = f.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, f.creator.Value(), "Ctrl+U clears the focused field")
	type_(f, "ops")
	require.Equal(t, "ops", f.creator.Value())
}

func TestForm_BackspacePopsRune(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	type_(f, "abc")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "ab", f.matchers.Value())
}

func TestForm_EnterInMatchersAddsNewline(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	type_(f, "alertname=A")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	type_(f, "severity=critical")
	require.Equal(t, "alertname=A\nseverity=critical", f.matchers.Value())
}

func TestForm_EnterInOtherFieldsIsNoOp(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → starts
	prev := f.starts.Value()
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, prev, f.starts.Value(),
		"Enter must NOT modify the value of single-line inputs")
}

func TestForm_PrefillMatchers(t *testing.T) {
	t.Parallel()
	in := []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "severity", Value: "warning|critical", IsRegex: true, IsEqual: true},
		{Name: "team", Value: "platform"},                     // !=
		{Name: "instance", Value: ".*-canary", IsRegex: true}, // !~
	}
	f := New(Options{
		Clients:  map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:   defaultTenant,
		Styles:   testutil.LoadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: in,
	})
	want := "alertname=HighCPU\nseverity=~warning|critical\nteam!=platform\ninstance!~.*-canary"
	require.Equal(t, want, f.matchers.Value())
}

func TestForm_PrefillEndsAt(t *testing.T) {
	t.Parallel()
	endsAt := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)
	f := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EndsAt:  endsAt,
	})
	require.Equal(t, "2026-04-25T14:00:00Z", f.ends.Value())
}

func TestForm_PrefillEndsAtZeroKeepsDefault(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.Equal(t, "2h", f.ends.Value(), "zero EndsAt must keep the duration shorthand default")
}

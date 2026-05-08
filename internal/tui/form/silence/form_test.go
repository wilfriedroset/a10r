// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

// fakeClient records every CreateSilence / UpdateSilence call so
// each test can assert which verb the form picked plus the spec
// it sent.
type fakeClient struct {
	last          backend.SilenceSpec
	createCalls   int
	updateCalls   int
	lastUpdateID  string
	wantID        string
	wantErr       error
	wantUpdateErr error
}

func (f *fakeClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	f.createCalls++
	f.last = spec
	return f.wantID, f.wantErr
}

func (f *fakeClient) UpdateSilence(_ context.Context, id string, spec backend.SilenceSpec) error {
	f.updateCalls++
	f.last = spec
	f.lastUpdateID = id
	return f.wantUpdateErr
}

// calls is the legacy "either verb" accessor used by tests written
// before the form learned edit mode.
func (f *fakeClient) calls() int { return f.createCalls + f.updateCalls }

func newForm(t *testing.T, client Client) *Form {
	t.Helper()
	return New(Options{
		Client:  client,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
}

// fillValid types a minimum-valid form into f. The caller drives
// Tab navigation between fields; this helper just types each
// field as if the cursor is already on it.
func type_(f *Form, s string) {
	for _, r := range s {
		_, _ = f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// drainSubmit walks the async submit cycle: fires Ctrl+S, runs the
// returned cmd to get the submitDoneMsg the worker goroutine would
// post, and feeds it back through Update so the second cmd carries
// the user-facing SubmittedMsg / FlashShowMsg. Validation failures
// short-circuit before submitDoneMsg — in that case the first cmd
// already carries the flash, and the second leg is skipped.
func drainSubmit(t *testing.T, f *Form) tea.Msg {
	t.Helper()
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "submit must return a cmd")
	first := cmd()
	done, ok := first.(submitDoneMsg)
	if !ok {
		// Validation / nil-client / bulk paths return their final
		// message directly — no async leg to drain.
		return first
	}
	_, cmd2 := f.Update(done)
	require.NotNil(t, cmd2, "submitDoneMsg handling must return a cmd")
	return cmd2()
}

func TestForm_DefaultEnds(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	require.Equal(t, "2h", f.ends.Value(), "default endsAt is +2h shorthand")
}

func TestForm_BlankEndsLeavesFieldEmpty(t *testing.T) {
	t.Parallel()
	// Recreate-expired entry point wants the user to type a fresh
	// duration; the "2h" default would be a footgun (one tap of
	// Ctrl+S and the silence comes back with the placeholder).
	f := New(Options{
		Client:    &fakeClient{},
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
		Client:    &fakeClient{},
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		EndsAt:    fixedNow.Add(time.Hour),
		BlankEnds: true,
	})
	require.Empty(t, f.ends.Value(), "BlankEnds wins over EndsAt prefill")
}

func TestForm_LegacyNFlowClearedEndsErrors(t *testing.T) {
	t.Parallel()
	// The parseEndsAt tightening (empty input → error) is a side
	// benefit for the regular `n` and `e` flows: a user who clears
	// the prefilled "2h" with backspace and presses Ctrl+S used to
	// silently get a base+2h fallback. Locking this so the silent
	// fallback stays gone if parseEndsAt is ever loosened again.
	client := &fakeClient{}
	f := newForm(t, client)
	type_(f, "alertname=A")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})            // starts
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})            // ends (prefilled "2h")
	_, _ = f.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) // clear
	require.Empty(t, f.ends.Value(), "Ctrl+U must clear the ends field for the test setup")
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, strings.ToLower(msg.Text), "ends",
		"cleared ends must error, not silently fall back to base+2h")
	require.Equal(t, 0, client.calls(),
		"form must not call the backend with a cleared ends value")
}

func TestForm_BlankEndsSubmitWithoutTypingErrors(t *testing.T) {
	t.Parallel()
	// BlankEnds is only useful as a footgun guard if the submit
	// path actually refuses an empty ends — otherwise the user
	// could press Ctrl+S right away and silently get the legacy
	// 2h fallback. Lock the validation: empty ends must surface a
	// flash and keep the form open so the user types a duration.
	client := &fakeClient{}
	f := New(Options{
		Client:    client,
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		Matchers:  []backend.Matcher{{Name: "alertname", Value: "X", IsEqual: true}},
		Comment:   "ack",
		BlankEnds: true,
	})
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "submit must produce a flash Cmd, not silently succeed")
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, strings.ToLower(msg.Text), "ends",
		"validation error must mention the ends field so the user knows where to look")
	require.Equal(t, 0, client.calls(), "form must not call the backend with an empty ends")
}

func TestForm_FocusEndsLandsOnEndsField(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Client:    &fakeClient{},
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		FocusEnds: true,
	})
	require.Equal(t, fieldEnds, f.focus, "FocusEnds lands focus on Ends")
	require.True(t, f.ends.Focused(), "FocusEnds focuses the ends input")
	require.False(t, f.matchers.Focused(), "FocusEnds blurs the default matchers field")
}

func TestForm_CreatorDefaultedFromOpts(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	require.Equal(t, "alice", f.creator.Value())
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

func TestForm_ErrorPersistsAcrossNavigation(t *testing.T) {
	t.Parallel()
	// A failed submit should leave the validation error visible
	// so the user can read which field broke and Tab over to fix
	// it. Wiping err on every keystroke (including Tab) defeats
	// that — the error must survive at least until the next
	// submit attempt.
	f := newForm(t, &fakeClient{})
	// Submit empty matchers → fail() sets f.err.
	_, _ = f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotEmpty(t, f.err, "validation failure must populate f.err")
	prev := f.err
	// Tab to the next field — error must stay.
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, prev, f.err, "Tab must not wipe the validation error")
	// Type into the next field — bubbles routes the keystroke,
	// the error stays so the user keeps the context.
	_, _ = f.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Equal(t, prev, f.err, "typing must not wipe the validation error")
}

func TestForm_FocusToggleBlursPrevious(t *testing.T) {
	t.Parallel()
	// Walking focus must Blur the outgoing field and Focus the
	// incoming one so bubbles renders the cursor on exactly one
	// input at a time.
	f := newForm(t, &fakeClient{})
	require.True(t, f.matchers.Focused())
	require.False(t, f.starts.Focused())

	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.False(t, f.matchers.Focused())
	require.True(t, f.starts.Focused())
}

func TestForm_CapturesInput(t *testing.T) {
	t.Parallel()
	// The form must opt into raw key capture so the App
	// bypasses LayerGlobal bindings (q / : / / / ? / 0-9) and
	// routes those keys into the field instead of quitting,
	// opening the prompt, or switching tenants.
	f := newForm(t, &fakeClient{})
	require.True(t, f.CapturesInput())
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

// blockingClient gates CreateSilence on a release channel so the
// test can prove submit() does not perform HTTP on the Update
// goroutine. If the form regresses to a synchronous call, Update
// blocks forever and the test deadlocks (caught by t.Deadline).
type blockingClient struct {
	gate    chan struct{}
	started chan struct{}
}

func (b *blockingClient) CreateSilence(_ context.Context, _ backend.SilenceSpec) (string, error) {
	close(b.started)
	<-b.gate
	return "sil-async", nil
}

func (b *blockingClient) UpdateSilence(_ context.Context, _ string, _ backend.SilenceSpec) error {
	close(b.started)
	<-b.gate
	return nil
}

func TestForm_SubmitDoesNotBlockUpdateGoroutine(t *testing.T) {
	t.Parallel()
	client := &blockingClient{
		gate:    make(chan struct{}),
		started: make(chan struct{}),
	}
	f := newForm(t, client)
	type_(f, "alertname=A")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack")

	// Update must return without waiting for CreateSilence.
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)

	// Run cmd on a goroutine so the test goroutine stays free.
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("CreateSilence never started — submit returned without scheduling the round-trip")
	}

	// Releasing the gate must let cmd produce submitDoneMsg.
	close(client.gate)
	select {
	case msg := <-done:
		_, ok := msg.(submitDoneMsg)
		require.True(t, ok, "expected submitDoneMsg, got %T", msg)
	case <-time.After(time.Second):
		t.Fatal("cmd never returned after gate released")
	}
}

func TestForm_SubmitSuccessEmitsSubmittedMsg(t *testing.T) {
	t.Parallel()
	client := &fakeClient{wantID: "sil-42"}
	f := newForm(t, client)

	type_(f, "alertname=HighCPU\nseverity=critical")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack while patching")

	msg := drainSubmit(t, f).(SubmittedMsg)
	require.Equal(t, "sil-42", msg.ID)
	require.Equal(t, 1, client.createCalls)
	require.Equal(t, 0, client.updateCalls)
	require.Equal(t, "alice", client.last.CreatedBy)
	require.Equal(t, "ack while patching", client.last.Comment)
	require.Len(t, client.last.Matchers, 2)
	require.Equal(t, fixedNow.Add(2*time.Hour), client.last.EndsAt)
}

func TestForm_SubmitWithoutMatchersFails(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	f := newForm(t, client)
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "comment ok")
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "matcher")
	require.Equal(t, 0, client.calls(), "submit must not reach client on validation failure")
}

func TestForm_SubmitWithBadMatcherShowsLine(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	f := newForm(t, client)
	type_(f, "alertname=A\nbroken")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "comment")
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "line 2")
}

func TestForm_SubmitClientErrorFlashesAndKeepsForm(t *testing.T) {
	t.Parallel()
	client := &fakeClient{wantErr: errors.New("boom")}
	f := newForm(t, client)
	type_(f, "alertname=A")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "comment")
	msg := drainSubmit(t, f).(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "boom")
	require.Equal(t, "boom", f.err,
		"the form must remember the error so the View can re-display it")
}

func TestForm_EscEmitsCancelled(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	_, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, ok := cmd().(CancelledMsg)
	require.True(t, ok)
}

func TestForm_EndsAtAcceptsRFC3339(t *testing.T) {
	t.Parallel()
	client := &fakeClient{wantID: "id"}
	f := newForm(t, client)
	type_(f, "alertname=A")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // starts
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // ends
	_, _ = f.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	type_(f, "2026-04-25T18:00:00Z")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // creator
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // comment
	type_(f, "ack")
	_, ok := drainSubmit(t, f).(SubmittedMsg)
	require.True(t, ok)
	require.Equal(t,
		time.Date(2026, 4, 25, 18, 0, 0, 0, time.UTC),
		client.last.EndsAt,
	)
}

func TestForm_MatcherOperators(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line    string
		want    backend.Matcher
		wantErr bool
	}{
		{line: "a=b", want: backend.Matcher{Name: "a", Value: "b", IsEqual: true}},
		{line: "a!=b", want: backend.Matcher{Name: "a", Value: "b"}},
		{line: "a=~.*", want: backend.Matcher{Name: "a", Value: ".*", IsRegex: true, IsEqual: true}},
		{line: "a!~.*", want: backend.Matcher{Name: "a", Value: ".*", IsRegex: true}},
		{line: "noop", wantErr: true},
		{line: "=oops", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			got, err := parseOneMatcher(tc.line)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestForm_EndsBeforeStartsRejects(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	f := newForm(t, client)
	type_(f, "alertname=A")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // starts
	type_(f, "2026-04-25T18:00:00Z")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // ends
	_, _ = f.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	type_(f, "2026-04-25T12:00:00Z")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // creator
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // comment
	type_(f, "ack")
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "ends must be after starts")
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
		Client:   &fakeClient{},
		Styles:   testutil.LoadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: in,
	})
	want := "alertname=HighCPU\nseverity=~warning|critical\nteam!=platform\ninstance!~.*-canary"
	require.Equal(t, want, f.matchers.Value())
}

func TestForm_PrefillComment(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Client:  &fakeClient{},
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		Comment: "ack while patching",
	})
	require.Equal(t, "ack while patching", f.comment.Value())
}

func TestForm_PrefillEndsAt(t *testing.T) {
	t.Parallel()
	endsAt := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)
	f := New(Options{
		Client:  &fakeClient{},
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
		Client:  &fakeClient{},
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.Equal(t, "2h", f.ends.Value(), "zero EndsAt must keep the duration shorthand default")
}

func TestForm_EditModeCallsUpdate(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	f := New(Options{
		Client:   client,
		Styles:   testutil.LoadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: []backend.Matcher{{Name: "alertname", Value: "A", IsEqual: true}},
		Comment:  "still ack",
		EditID:   "sil-7",
	})

	msg, ok := drainSubmit(t, f).(SubmittedMsg)
	require.True(t, ok)
	require.Equal(t, "sil-7", msg.ID, "SubmittedMsg in edit mode echoes the EditID")
	require.True(t, msg.Updated, "Updated must be true so parent page flashes \"updated\" not \"created\"")
	require.Equal(t, 1, client.updateCalls)
	require.Equal(t, 0, client.createCalls)
	require.Equal(t, "sil-7", client.lastUpdateID)
	require.Equal(t, "still ack", client.last.Comment)
}

func TestForm_EditModeClientErrorFlashesAndKeepsForm(t *testing.T) {
	t.Parallel()
	client := &fakeClient{wantUpdateErr: errors.New("update boom")}
	f := New(Options{
		Client:   client,
		Styles:   testutil.LoadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: []backend.Matcher{{Name: "alertname", Value: "A", IsEqual: true}},
		Comment:  "ack",
		EditID:   "sil-7",
	})
	msg, ok := drainSubmit(t, f).(footer.FlashShowMsg)
	require.True(t, ok)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "update boom")
}

func TestForm_FormatMatchersRoundTrip(t *testing.T) {
	t.Parallel()
	// Includes values that themselves contain an operator-like
	// substring (`a!=b`, `a=b`, `=~regex`) so the leftmost-position
	// parser is exercised — alerts in the wild can carry such
	// values in annotations, and a lossy round-trip would silently
	// rewrite the matcher when the user opens an `e` form.
	in := []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "severity", Value: "warning|critical", IsRegex: true, IsEqual: true},
		{Name: "team", Value: "platform"},
		{Name: "instance", Value: ".*-canary", IsRegex: true},
		{Name: "expr", Value: "a!=b", IsEqual: true},
		{Name: "expr2", Value: "a=b", IsEqual: true},
		{Name: "expr3", Value: "x=~y", IsEqual: false},
	}
	rendered := formatMatchers(in)
	parsed, err := parseMatchers(rendered)
	require.NoError(t, err)
	require.Equal(t, in, parsed)
}

func TestForm_ParseOneMatcherLeftmostWins(t *testing.T) {
	t.Parallel()
	// Direct exercise of the leftmost-position rule. `foo=a!=b`
	// must split on the first `=`, not the later `!=`. Tie at
	// the same index between `=~` and `=` resolves to `=~`.
	cases := []struct {
		line string
		want backend.Matcher
	}{
		{
			line: "foo=a!=b",
			want: backend.Matcher{Name: "foo", Value: "a!=b", IsEqual: true},
		},
		{
			line: "foo=~bar",
			want: backend.Matcher{Name: "foo", Value: "bar", IsRegex: true, IsEqual: true},
		},
		{
			line: "foo!~bar=baz",
			want: backend.Matcher{Name: "foo", Value: "bar=baz", IsRegex: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			got, err := parseOneMatcher(tc.line)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMatchersFromLabels_DropsNameAndSorts(t *testing.T) {
	t.Parallel()
	got := MatchersFromLabels(map[string]string{
		"__name__":  "ALERTS",
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "host-1",
	})
	require.Equal(t, []backend.Matcher{
		{Name: "alertname", Value: "HighCPU", IsEqual: true},
		{Name: "instance", Value: "host-1", IsEqual: true},
		{Name: "severity", Value: "critical", IsEqual: true},
	}, got, "synthetic __name__ must be dropped; output stable-sorted by name")
}

func TestMatchersFromLabels_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := MatchersFromLabels(map[string]string{})
	require.Empty(t, got)
}

func TestForm_TitleSwitchesOnEditID(t *testing.T) {
	t.Parallel()
	create := New(Options{
		Client:  &fakeClient{},
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	edit := New(Options{
		Client:  &fakeClient{},
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EditID:  "sil-7",
	})
	require.Equal(t, "new silence", create.Title())
	require.Equal(t, "edit silence sil-7", edit.Title())
}

// panickingClient fails any test that drives the form into a Client
// call. Used by the bulk-mode tests to prove the form never reaches
// the write surface — bulk fanout is the page's job, not the form's.
type panickingClient struct{}

func (panickingClient) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	panic("CreateSilence must not be called in bulk mode")
}

func (panickingClient) UpdateSilence(context.Context, string, backend.SilenceSpec) error {
	panic("UpdateSilence must not be called in bulk mode")
}

func newBulkForm(t *testing.T, client Client, banner string) *Form {
	t.Helper()
	return New(Options{
		Client:     client,
		Styles:     testutil.LoadStyles(t),
		Now:        func() time.Time { return fixedNow },
		Creator:    "alice",
		Bulk:       true,
		BulkBanner: banner,
	})
}

func TestForm_BulkModeTitle(t *testing.T) {
	t.Parallel()

	f := newBulkForm(t, &fakeClient{}, "applies to 5 alerts across 2 tenants — each silenced with its own labels")
	require.Equal(t, "bulk silence", f.Title(),
		"bulk mode wins over create/edit; the banner carries the count breakdown")
}

func TestForm_BulkModeStartsFocusOnStarts(t *testing.T) {
	t.Parallel()

	// fieldMatchers is hidden in bulk mode, so initial focus must
	// land on the first visible field. Tab walks the four metadata
	// fields in a closed loop without ever touching matchers.
	f := newBulkForm(t, nil, "banner")
	require.Equal(t, fieldStarts, f.focus, "bulk mode starts on Starts; matchers is hidden")
}

func TestForm_BulkModeTabSkipsMatcherField(t *testing.T) {
	t.Parallel()

	// Closed cycle of four fields: Starts → Ends → Creator → Comment
	// → Starts. Matchers must never appear.
	f := newBulkForm(t, nil, "banner")
	want := []fieldIndex{fieldEnds, fieldCreator, fieldComment, fieldStarts, fieldEnds}
	for i, expected := range want {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equalf(t, expected, f.focus, "tab #%d landed on the wrong field (expected %v, got %v)", i+1, expected, f.focus)
	}

	// Shift+Tab walks the same closed cycle in reverse — must also
	// skip matchers.
	f2 := newBulkForm(t, nil, "banner")
	require.Equal(t, fieldStarts, f2.focus)
	_, _ = f2.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, fieldComment, f2.focus,
		"shift+tab from Starts must skip matchers and land on Comment")
}

func TestForm_BulkModeSkipsMatcherValidation(t *testing.T) {
	t.Parallel()

	// In bulk mode, leaving the matchers buffer empty must not
	// error — the page substitutes per-target matchers at fanout.
	// Create + comment + submit and assert no matcher-related error.
	f := newBulkForm(t, panickingClient{}, "banner")
	// Tab past Starts/Ends/Creator to Comment, then type.
	for range 3 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	require.Equal(t, fieldComment, f.focus)
	type_(f, "ack while patching")

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(BulkSubmittedMsg)
	require.True(t, ok, "submit must succeed and emit BulkSubmittedMsg, got %T", msg)
}

func TestForm_BulkModeEmitsBulkSubmittedMsg(t *testing.T) {
	t.Parallel()

	f := newBulkForm(t, panickingClient{}, "banner")
	for range 3 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack while patching")

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	got, ok := cmd().(BulkSubmittedMsg)
	require.True(t, ok)
	require.Equal(t, "ack while patching", got.Comment)
	require.Equal(t, "alice", got.Creator)
	require.Equal(t, fixedNow, got.StartsAt, "default starts is the injected now")
	require.Equal(t, fixedNow.Add(2*time.Hour), got.EndsAt, "default ends is +2h")
}

func TestForm_BulkModeNeverCallsClient(t *testing.T) {
	t.Parallel()

	// Same shape as BulkModeEmitsBulkSubmittedMsg but with the
	// panicking client wired in. If submit ever reaches Client.*
	// the test panics and fails.
	f := newBulkForm(t, panickingClient{}, "banner")
	for range 3 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack")

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.NotPanics(t, func() {
		_ = cmd()
	}, "bulk submit must not call Client.* — the page owns dispatch")
}

func TestForm_BulkModeNilClientIsAllowed(t *testing.T) {
	t.Parallel()

	// Pages opening the bulk form may legitimately pass Client = nil
	// because the form never dispatches in bulk mode. A nil client
	// must round-trip through submit without the "client not
	// configured" failure path tripping.
	f := newBulkForm(t, nil, "banner")
	for range 3 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack")

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	_, ok := cmd().(BulkSubmittedMsg)
	require.True(t, ok, "nil-client bulk submit must still emit BulkSubmittedMsg")
}

func TestForm_BulkModeRendersBanner(t *testing.T) {
	t.Parallel()

	// Pick a banner short enough to fit one line inside the
	// 120-col View — long banners wrap to the input width, which
	// is correct behaviour but would break a literal substring
	// match. Real-world banners (e.g. "applies to 5 alerts
	// across 2 tenants — each silenced with its own labels") may
	// well wrap; the wrap shape is incidental, the verbatim
	// presence in the rendered view is what we care about.
	banner := "applies to 5 alerts; per-target labels"
	f := newBulkForm(t, nil, banner)

	view := f.View(120, 24)
	require.Contains(t, view, banner, "bulk View must render the banner string verbatim")
	require.Contains(t, view, "Targets", "bulk View labels the slot 'Targets' so the user knows the matchers are per-target")
	require.NotContains(t, view, "alertname=HighCPU",
		"bulk View must NOT render the matchers placeholder — the buffer is hidden")
}

func TestForm_BulkModeIgnoresPrefilledMatchers(t *testing.T) {
	t.Parallel()

	// A caller passing both Bulk and Matchers is contradictory; the
	// form must not leak the matcher prefill into the hidden buffer.
	// This guards a future regression where a caller copies an Options
	// struct and forgets to clear Matchers.
	f := New(Options{
		Styles:     testutil.LoadStyles(t),
		Now:        func() time.Time { return fixedNow },
		Creator:    "alice",
		Bulk:       true,
		BulkBanner: "banner",
		Matchers: []backend.Matcher{
			{Name: "alertname", Value: "HighCPU", IsEqual: true},
		},
	})
	require.Empty(t, f.matchers.Value(), "bulk mode must not prefill the hidden matchers buffer")
}

func TestForm_BulkSubmittedMsgIsAutoPop(t *testing.T) {
	t.Parallel()

	// Pinning the AutoPopMsg contract — the App's stack only auto-pops
	// messages that satisfy the marker interface. Without this assertion
	// a future rename breaks the form's submit-and-pop UX silently.
	var msg interface{ IsAutoPop() } = BulkSubmittedMsg{}
	require.NotNil(t, msg)
}

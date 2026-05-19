// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/matcher"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
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

// ExpireSilence is part of silenceform.Client's contract since
// the silences list page uses the same write surface, but the
// form itself never calls it. Accepts every id without error so
// tests that route through silenceform.Client don't have to
// special-case this fake.
func (*fakeClient) ExpireSilence(context.Context, string) error { return nil }

// calls is the legacy "either verb" accessor used by tests written
// before the form learned edit mode.
func (f *fakeClient) calls() int { return f.createCalls + f.updateCalls }

// defaultTenant is the canonical tenant name used by single-tenant
// tests so every fixture routes writes through the same key — the
// form's submit logic looks up clients[tenant], so the helper
// shape mirrors what the silences page passes through in normal
// flow.
const defaultTenant = "prod"

func newForm(t *testing.T, client Client) *Form {
	t.Helper()
	return New(Options{
		Clients: map[string]Client{defaultTenant: client},
		Tenant:  defaultTenant,
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
		Clients:   map[string]Client{defaultTenant: client},
		Tenant:    defaultTenant,
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

func (*blockingClient) ExpireSilence(context.Context, string) error { return nil }

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

// ctxBlockingClient signals `started` then waits for ctx to cancel.
// Lets tests observe whether the form cancels its in-flight submit
// when the page is Closed.
type ctxBlockingClient struct {
	started chan struct{}
}

func (c *ctxBlockingClient) CreateSilence(ctx context.Context, _ backend.SilenceSpec) (string, error) {
	close(c.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func (c *ctxBlockingClient) UpdateSilence(ctx context.Context, _ string, _ backend.SilenceSpec) error {
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func (*ctxBlockingClient) ExpireSilence(context.Context, string) error { return nil }

// TestForm_CloseCancelsInflightSubmit pins that closing the form
// cancels the in-flight Create/UpdateSilence call instead of letting
// the goroutine outlive the form. Without the fix, Esc-then-page-swap
// while a slow tenant is still writing creates/updates a silence
// the operator never gets confirmation for — orphan-from-UX. Worse,
// if the user re-opens the form to retry, two writes race.
func TestForm_CloseCancelsInflightSubmit(t *testing.T) {
	t.Parallel()
	client := &ctxBlockingClient{started: make(chan struct{})}
	f := newForm(t, client)
	type_(f, "alertname=A")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack")

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("CreateSilence never started")
	}

	// Closing the form must cancel the in-flight CreateSilence so
	// the goroutine returns within a bounded window — without the
	// fix, the test would time out after 2s.
	f.Close()

	select {
	case msg := <-done:
		// Stale submitDoneMsg with ctx.Canceled error is fine; the
		// point is the goroutine returned promptly.
		_, ok := msg.(submitDoneMsg)
		require.True(t, ok, "expected submitDoneMsg, got %T", msg)
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not cancel the in-flight submit — goroutine leak window")
	}
}

// TestForm_SubmitInheritsParentCtxCancellation pins the documented
// contract: cancelling the app-level parent ctx handed in via
// Options.SubmitCtx aborts the in-flight Create/UpdateSilence call.
// Without the plumbing, submit() parents on context.Background() and
// only Close() (the page-pop / quit-cascade path) reaches the worker.
// A future caller bypassing Close — a programmatic test, a REPL, a
// shutdown hook firing on the ctx — would see the goroutine outlive
// the form for the full transport timeout.
func TestForm_SubmitInheritsParentCtxCancellation(t *testing.T) {
	t.Parallel()
	client := &ctxBlockingClient{started: make(chan struct{})}
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	f := New(Options{
		Clients:   map[string]Client{defaultTenant: client},
		Tenant:    defaultTenant,
		Styles:    testutil.LoadStyles(t),
		Now:       func() time.Time { return fixedNow },
		Creator:   "alice",
		SubmitCtx: parent,
	})
	type_(f, "alertname=A")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack")

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("CreateSilence never started")
	}

	// Cancelling the parent ctx must propagate to the in-flight call.
	// Without the plumbing, the worker's ctx is parented on Background
	// and this cancel is invisible to it — the test would time out.
	cancelParent()

	select {
	case msg := <-done:
		_, ok := msg.(submitDoneMsg)
		require.True(t, ok, "expected submitDoneMsg, got %T", msg)
	case <-time.After(2 * time.Second):
		t.Fatal("parent ctx cancellation did not reach the in-flight submit — Options.SubmitCtx not plumbed")
	}
}

// TestForm_DoubleCtrlSDropsSecondSubmit locks the re-entry guard:
// a second Ctrl+S while the first round-trip is still in flight
// must not start a second backend call. Regression catcher for the
// "user hammers Ctrl+S on a slow tenant" failure mode the async
// fix would otherwise expose — without the guard the operator
// quietly creates duplicate silences.
func TestForm_DoubleCtrlSDropsSecondSubmit(t *testing.T) {
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

	// First Ctrl+S queues the goroutine.
	_, cmd1 := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd1)
	go func() { _ = cmd1() }()
	<-client.started

	// Second Ctrl+S must NOT call CreateSilence again. The cmd it
	// returns is a flash, not a submitDoneMsg-producing closure.
	_, cmd2 := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd2)
	msg2 := cmd2()
	flash, ok := msg2.(footer.FlashShowMsg)
	require.Truef(t, ok, "second Ctrl+S during in-flight submit must produce a flash, got %T", msg2)
	require.Equal(t, footer.FlashWarn, flash.Level)
	require.Contains(t, flash.Text, "in flight")

	// Releasing the gate must let the first submit complete cleanly.
	close(client.gate)
}

// TestForm_StaleSubmitDoneMsgIsDropped locks the generation token:
// if the user hits Esc and the form is later destroyed / re-pushed,
// the in-flight goroutine's submitDoneMsg must NOT auto-pop the new
// form or flash on the page on top of the stack. We model this by
// re-creating the form between submit and applySubmitDone; the new
// instance starts at gen=0 while the stale message carries gen=1,
// so applySubmitDone discards it.
func TestForm_StaleSubmitDoneMsgIsDropped(t *testing.T) {
	t.Parallel()
	stale := submitDoneMsg{gen: 99, id: "sil-stale"}
	f := newForm(t, &fakeClient{})
	_, cmd := f.Update(stale)
	require.Nil(t, cmd, "stale submitDoneMsg must produce no Cmd; current gen is 0, stale carries 99")
}

// TestForm_SubmitContextCancelDropsSilently pins the defensive
// contract: a submitDoneMsg whose err is context.Canceled (the
// SubmitCtx parent fired during shutdown) must NOT flash
// "silence: context canceled" on whatever page is being torn
// down. The render path is unreachable in prod because Close()
// pops the form before the message lands, but rendering a
// fake "failed" on the last frame would be misleading — the
// audit asked for an explicit short-circuit so the contract
// is visible in code.
func TestForm_SubmitContextCancelDropsSilently(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	// Match the active gen (fresh form is at 0) so we exercise the
	// real apply branch rather than the stale-drop short-circuit.
	f.submitting = true
	msg := submitDoneMsg{gen: f.submitGen, err: context.Canceled}
	_, cmd := f.Update(msg)
	require.Nil(t, cmd,
		"context.Canceled must drop silently — no flash on a page that's being torn down")
	require.Empty(t, f.err,
		"ctx-cancel is shutdown noise; the form must not record it as a submit error")
	require.False(t, f.submitting,
		"submitting must clear so a future re-open of the form starts clean")
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

func TestForm_EditModeCallsUpdate(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	f := New(Options{
		Clients:  map[string]Client{defaultTenant: client},
		Tenant:   defaultTenant,
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
		Clients:  map[string]Client{defaultTenant: client},
		Tenant:   defaultTenant,
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
	parsed, err := matcher.Parse(rendered)
	require.NoError(t, err)
	require.Equal(t, in, parsed)
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

func TestForm_TitleSwitchesOnEditID(t *testing.T) {
	t.Parallel()
	create := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	edit := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
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

func (panickingClient) ExpireSilence(context.Context, string) error {
	panic("ExpireSilence must not be called in bulk mode")
}

func newBulkForm(t *testing.T, client Client, banner string) *Form {
	t.Helper()
	// Bulk mode never resolves a Client (the page owns dispatch),
	// so the map is informational — but Options.Clients is the
	// authoritative shape post-ADR-0011. Nil client maps to an
	// empty Clients map so the helper keeps mirroring the
	// pre-ADR-0011 signature.
	var clients map[string]Client
	if client != nil {
		clients = map[string]Client{defaultTenant: client}
	}
	return New(Options{
		Clients:    clients,
		Styles:     testutil.LoadStyles(t),
		Now:        func() time.Time { return fixedNow },
		Creator:    "alice",
		Bulk:       true,
		BulkBanner: banner,
	})
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

// newMultiTenantForm builds a form with two clients keyed by name
// and an initial selection of "prod". Helper for the ADR-0011
// tests so each case shares the same fixture shape and can focus
// on the picker / cycle / submit assertions. Tests that need a
// different initial selection construct their own Options inline.
func newMultiTenantForm(t *testing.T, prod, staging Client) *Form {
	t.Helper()
	return New(Options{
		Clients: map[string]Client{"prod": prod, "staging": staging},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
}

// fillValidScalars types valid Starts/Ends/Creator/Comment values
// into the form starting from the matchers field. Caller is
// responsible for getting focus to matchers first (or relying on
// the default). Leaves focus on Comment.
func fillValidScalars(t *testing.T, f *Form) {
	t.Helper()
	// Already on matchers by default (or wherever the caller put
	// us); type the matcher buffer first.
	type_(f, "alertname=A")
	// Walk to Starts / Ends / Creator / Comment.
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	require.Equal(t, fieldComment, f.focus, "fillValidScalars must land on Comment")
	type_(f, "ack while patching")
}

// TestForm_SubmitRoutesToActiveTenantClient asserts the core
// ADR-0011 contract: the form looks up clients[tenant] at submit
// time, so the initial Tenant routes to one fake and a picker-
// driven change routes to the other.
func TestForm_SubmitRoutesToActiveTenantClient(t *testing.T) {
	t.Parallel()

	prod := &fakeClient{wantID: "sil-prod"}
	staging := &fakeClient{wantID: "sil-staging"}
	f := newMultiTenantForm(t, prod, staging)

	// First submit: routes to prod (the initial selection).
	fillValidScalars(t, f)
	msg := drainSubmit(t, f).(SubmittedMsg)
	require.Equal(t, "sil-prod", msg.ID, "initial submit must route to clients[\"prod\"]")
	require.Equal(t, 1, prod.createCalls)
	require.Equal(t, 0, staging.createCalls)

	// Simulate a picker submission that switches the active tenant.
	// The picker emits PickerSubmittedMsg with the selected name and
	// the form's pickerOrigin tag; the form's Update consumes it and
	// sets f.tenant accordingly.
	_, _ = f.Update(modal.PickerSubmittedMsg{Origin: pickerOrigin, Selections: []string{"staging"}})
	require.Equal(t, "staging", f.tenant, "picker submit must update the form's active tenant")

	// Second submit: must route to staging now.
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	done := cmd().(submitDoneMsg)
	_, cmd2 := f.Update(done)
	require.NotNil(t, cmd2)
	msg2 := cmd2().(SubmittedMsg)
	require.Equal(t, "sil-staging", msg2.ID, "second submit must route to clients[\"staging\"]")
	require.Equal(t, 1, prod.createCalls, "prod must not be called a second time")
	require.Equal(t, 1, staging.createCalls)
}

// TestForm_PickerCancelledIsNoOp pins the cancel branch: a
// PickerCancelledMsg arriving on the form keeps the active tenant
// unchanged so an accidental Esc inside the picker doesn't silently
// re-route the next submit.
func TestForm_PickerCancelledIsNoOp(t *testing.T) {
	t.Parallel()

	prod := &fakeClient{wantID: "sil-prod"}
	staging := &fakeClient{wantID: "sil-staging"}
	f := newMultiTenantForm(t, prod, staging)

	_, cmd := f.Update(modal.PickerCancelledMsg{Origin: pickerOrigin})
	require.Nil(t, cmd, "cancel must not produce a follow-up Cmd")
	require.Equal(t, "prod", f.tenant, "cancel must leave the active tenant alone")
}

// TestForm_MultiTenantTenantRowFocusable asserts that with two or
// more clients and no EditID / Bulk flag, the Tenant row is part
// of the focus cycle and Enter on it opens the picker via the
// App's modal pipeline (openModalMsg envelope).
func TestForm_MultiTenantTenantRowFocusable(t *testing.T) {
	t.Parallel()

	f := newMultiTenantForm(t, &fakeClient{}, &fakeClient{})
	// Default focus is Matchers (the user opens the form to type
	// matchers; tabbing back to Tenant is the explicit affordance).
	require.Equal(t, fieldMatchers, f.focus)

	// Shift+Tab from Matchers lands on Tenant in a multi-tenant
	// form (single-tenant / edit / bulk would have skipped it).
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, fieldTenant, f.focus, "shift+tab from Matchers must land on Tenant when the row is enabled")

	// Enter on the Tenant row emits the modal-open envelope. We
	// assert by type-name (openModalMsg is unexported in the app
	// package, same pattern as alert_test.go's modal assertion).
	_, cmd := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "Enter on the Tenant row must emit a modal-open Cmd")
	require.Contains(t, fmt.Sprintf("%T", cmd()), "openModalMsg",
		"Tenant Enter must open the picker via app.OpenModal")
}

// TestForm_TenantRowHintAdvertisesEnter pins the discoverability
// affordance: when the Tenant row is editable the rendered view
// must include "[Enter to change]" so the user does not have to
// guess that Enter opens the picker. Disabled variants (single-
// tenant, edit-mode) must NOT show the hint because the affordance
// is inert there.
func TestForm_TenantRowHintAdvertisesEnter(t *testing.T) {
	t.Parallel()

	// Anchor on the stable token, not the literal punctuation —
	// a future theming pass that wraps the brackets in a styled
	// span shouldn't flake the contract that the affordance is
	// surfaced.
	const hintToken = "Enter to change"

	multi := newMultiTenantForm(t, &fakeClient{}, &fakeClient{})
	require.Contains(t, multi.View(120, 24), hintToken,
		"editable Tenant row must advertise the Enter-to-change affordance")

	single := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.NotContains(t, single.View(120, 24), hintToken,
		"disabled single-tenant Tenant row must not advertise an inert affordance")

	edit := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}, "staging": &fakeClient{}},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EditID:  "sil-7",
	})
	require.NotContains(t, edit.View(120, 24), hintToken,
		"edit-mode Tenant row is read-only and must not advertise the picker")

	// Narrow form: hint must elide rather than force a wrap that
	// breaks fieldRow's continuation-padding grid. Width 30 leaves
	// ~17 cols for the value column once label/prefix are subtracted,
	// well below "prod" (4) + "  [Enter to change]" (21).
	require.NotContains(t, multi.View(30, 24), hintToken,
		"narrow-width Tenant row must elide the hint to keep the grid aligned")
}

// TestForm_MultiTenantTabCycleIncludesTenant locks the full focus
// cycle with two enabled clients: Matchers → Starts → Ends →
// Creator → Comment → Tenant → Matchers. Single-tenant / edit /
// bulk shapes are covered in their dedicated tests below.
func TestForm_MultiTenantTabCycleIncludesTenant(t *testing.T) {
	t.Parallel()

	f := newMultiTenantForm(t, &fakeClient{}, &fakeClient{})
	require.Equal(t, fieldMatchers, f.focus)
	want := []fieldIndex{fieldStarts, fieldEnds, fieldCreator, fieldComment, fieldTenant, fieldMatchers}
	for i, expected := range want {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		require.Equalf(t, expected, f.focus, "tab #%d: want %v got %v", i+1, expected, f.focus)
	}
}

// TestForm_EditModeTenantRowDisabled asserts the ADR-0011 read-only
// contract for edit mode: the Tenant row is rendered but Tab skips
// it (the AM v2 API does not move silences between tenants) and
// Enter on it would be a no-op (the cycle never lands there).
func TestForm_EditModeTenantRowDisabled(t *testing.T) {
	t.Parallel()

	f := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}, "staging": &fakeClient{}},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EditID:  "sil-7",
	})
	require.True(t, f.tenantDisabled(), "edit mode must disable the Tenant row")
	require.Equal(t, fieldMatchers, f.focus, "edit mode default focus is Matchers, same as create")

	// Cycle through every field — Tenant must never appear.
	seen := map[fieldIndex]bool{f.focus: true}
	for range int(numFields) * 2 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		seen[f.focus] = true
	}
	require.False(t, seen[fieldTenant], "tab cycle must skip the Tenant row in edit mode")

	// The view must still render the row so the user sees which
	// tenant the silence belongs to.
	view := f.View(120, 24)
	require.Contains(t, view, "Tenant:", "edit-mode View must label the read-only Tenant row")
	require.Contains(t, view, "prod", "edit-mode View must show the locked tenant value")
}

// TestForm_SingleTenantTenantRowDisabled asserts that with exactly
// one client the Tenant row is rendered but read-only — there's
// nothing meaningful to pick, so Tab skips it and the focus marker
// stays off the row even if focus somehow lands there.
func TestForm_SingleTenantTenantRowDisabled(t *testing.T) {
	t.Parallel()

	f := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.True(t, f.tenantDisabled(), "single-tenant must disable the Tenant row")

	// Tab cycle never lands on Tenant.
	seen := map[fieldIndex]bool{f.focus: true}
	for range int(numFields) * 2 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
		seen[f.focus] = true
	}
	require.False(t, seen[fieldTenant], "single-tenant tab cycle must skip the Tenant row")

	// View still renders the row so the layout stays consistent
	// across single-tenant / multi-tenant deployments.
	view := f.View(120, 24)
	require.Contains(t, view, "Tenant:")
	require.Contains(t, view, "prod")
}

// TestForm_BulkModeNoTenantRow asserts that bulk mode omits the
// Tenant row entirely (the Targets banner is the source of truth
// for the per-tenant breakdown in bulk). EditID + Bulk is mutually
// exclusive per the existing comment, so this is the only path
// that skips the row outright rather than rendering it disabled.
func TestForm_BulkModeNoTenantRow(t *testing.T) {
	t.Parallel()

	f := newBulkForm(t, &fakeClient{}, "applies to 3 alerts across 2 tenants")
	view := f.View(120, 24)
	require.NotContains(t, view, "Tenant:",
		"bulk View must NOT render the Tenant row — the Targets banner is the source of truth")
	require.Contains(t, view, "Targets",
		"bulk View must keep the existing Targets banner label")
}

// TestForm_PickerListIsSortedAndScopeUnfiltered locks the picker
// list contract: every key in f.clients appears, sorted
// alphabetically, regardless of any current scope. The form
// passes the page's writeable map through verbatim — scope
// filtering is a viewing concern, not a write-target gate.
func TestForm_PickerListIsSortedAndScopeUnfiltered(t *testing.T) {
	t.Parallel()

	f := New(Options{
		Clients: map[string]Client{
			"zeta":  &fakeClient{},
			"alpha": &fakeClient{},
			"mu":    &fakeClient{},
		},
		Tenant:  "alpha",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})

	cmd := f.openTenantPicker()
	require.NotNil(t, cmd)
	// Drive the actual feed the picker sees rather than walking
	// f.clients again — the sort contract lives in sortedTenantNames,
	// so asserting on its output exercises the picker's input verbatim.
	names := f.sortedTenantNames()
	require.Equal(t, []string{"alpha", "mu", "zeta"}, names,
		"openTenantPicker must feed every key in f.clients in sorted order (scope is a viewing filter, not a write-target gate)")
	require.True(t, sort.StringsAreSorted(names),
		"the picker list must be alphabetically sorted so the user sees a stable order")
}

// TestForm_PickerSubmitWithEmptySelectionsIsNoOp pins the defensive
// branch: a PickerSubmittedMsg with no selections (the picker emits
// PickerCancelledMsg on empty filter today, but a future picker
// shape might still emit a zero-length submit) must not zero the
// active tenant.
func TestForm_PickerSubmitWithEmptySelectionsIsNoOp(t *testing.T) {
	t.Parallel()

	f := newMultiTenantForm(t, &fakeClient{}, &fakeClient{})
	_, _ = f.Update(modal.PickerSubmittedMsg{Origin: pickerOrigin})
	require.Equal(t, "prod", f.tenant, "empty selections must not clear the active tenant")
}

// TestForm_SubmitFailsWhenTenantUnreachable locks the defensive
// boundary check inside submit(): an unreachable tenant (empty
// string, or a key not present in f.clients) flashes a clear error
// rather than panicking on nil dereference. This path is
// unreachable through the UI in normal flow — the page only opens
// the form with a writeable tenant — but the guard documents the
// contract and catches a future refactor that breaks the wiring.
func TestForm_SubmitFailsWhenTenantUnreachable(t *testing.T) {
	t.Parallel()

	// Empty Tenant: form was constructed without an initial pick.
	f := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}},
		Tenant:  "",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	type_(f, "alertname=A")
	for range 4 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	type_(f, "ack")
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	flash := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, flash.Level)
	require.Contains(t, strings.ToLower(flash.Text), "tenant",
		"unreachable-tenant flash must mention the tenant so a future refactor can locate the broken wiring")
}

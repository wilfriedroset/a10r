// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

// fakeClient records every CreateSilence call.
type fakeClient struct {
	last    backend.SilenceSpec
	calls   int
	wantID  string
	wantErr error
}

func (f *fakeClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	f.calls++
	f.last = spec
	return f.wantID, f.wantErr
}

func newForm(t *testing.T, client Client) *Form {
	t.Helper()
	return New(Options{
		Client:  client,
		Styles:  loadStyles(t),
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

func TestForm_DefaultEnds(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	require.Equal(t, "2h", f.ends, "default endsAt is +2h shorthand")
}

func TestForm_CreatorDefaultedFromOpts(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	require.Equal(t, "alice", f.creator)
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

func TestForm_TypingAppendsToFocusedField(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	type_(f, "alertname=HighCPU")
	require.Equal(t, "alertname=HighCPU", f.matchers)

	// Tab to creator and overtype.
	for range 3 {
		_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	require.Equal(t, fieldCreator, f.focus)
	_, _ = f.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Empty(t, f.creator, "Ctrl+U clears the focused field")
	type_(f, "ops")
	require.Equal(t, "ops", f.creator)
}

func TestForm_BackspacePopsRune(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	type_(f, "abc")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "ab", f.matchers)
}

func TestForm_EnterInMatchersAddsNewline(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	type_(f, "alertname=A")
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	type_(f, "severity=critical")
	require.Equal(t, "alertname=A\nseverity=critical", f.matchers)
}

func TestForm_EnterInOtherFieldsIsNoOp(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → starts
	prev := f.starts
	_, _ = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, prev, f.starts, "Enter must NOT add a newline outside Matchers")
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

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg := cmd().(SubmittedMsg)
	require.Equal(t, "sil-42", msg.ID)
	require.Equal(t, 1, client.calls)
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
	require.Equal(t, 0, client.calls, "submit must not reach client on validation failure")
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
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
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
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	_, ok := cmd().(SubmittedMsg)
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

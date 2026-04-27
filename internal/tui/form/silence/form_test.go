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
		Styles:   loadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: in,
	})
	want := "alertname=HighCPU\nseverity=~warning|critical\nteam!=platform\ninstance!~.*-canary"
	require.Equal(t, want, f.matchers)
}

func TestForm_PrefillComment(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Client:  &fakeClient{},
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		Comment: "ack while patching",
	})
	require.Equal(t, "ack while patching", f.comment)
}

func TestForm_PrefillEndsAt(t *testing.T) {
	t.Parallel()
	endsAt := time.Date(2026, 4, 25, 14, 0, 0, 0, time.UTC)
	f := New(Options{
		Client:  &fakeClient{},
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EndsAt:  endsAt,
	})
	require.Equal(t, "2026-04-25T14:00:00Z", f.ends)
}

func TestForm_PrefillEndsAtZeroKeepsDefault(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Client:  &fakeClient{},
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.Equal(t, "2h", f.ends, "zero EndsAt must keep the duration shorthand default")
}

func TestForm_EditModeCallsUpdate(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	f := New(Options{
		Client:   client,
		Styles:   loadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: []backend.Matcher{{Name: "alertname", Value: "A", IsEqual: true}},
		Comment:  "still ack",
		EditID:   "sil-7",
	})

	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	msg, ok := cmd().(SubmittedMsg)
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
		Styles:   loadStyles(t),
		Now:      func() time.Time { return fixedNow },
		Creator:  "alice",
		Matchers: []backend.Matcher{{Name: "alertname", Value: "A", IsEqual: true}},
		Comment:  "ack",
		EditID:   "sil-7",
	})
	_, cmd := f.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	msg, ok := cmd().(footer.FlashShowMsg)
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
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	edit := New(Options{
		Client:  &fakeClient{},
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EditID:  "sil-7",
	})
	require.Equal(t, "new silence", create.Title())
	require.Equal(t, "edit silence sil-7", edit.Title())
}

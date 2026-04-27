// SPDX-License-Identifier: Apache-2.0

package silences

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
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/modal"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

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

func newPage(t *testing.T) *Page {
	t.Helper()
	return New(Options{
		Styles: loadStyles(t),
		Now:    func() time.Time { return fixedNow },
	})
}

func sil(id, by string, state backend.SilenceState, endsIn time.Duration) backend.Silence { //nolint:unparam // state kept for future tests covering pending / expired silences
	return backend.Silence{
		ID:        id,
		CreatedBy: by,
		State:     state,
		StartsAt:  fixedNow.Add(-time.Hour),
		EndsAt:    fixedNow.Add(endsIn),
	}
}

func TestPage_DefaultsToEndsAtAscending(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, SortByEndsAt, p.sort)
	require.True(t, p.sortAsc, "soonest-expiring first matches operator priority")
}

func TestPage_DataMsgPopulatesAndSortsByEndsAtAscending(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	silences := []backend.Silence{
		sil("late", "alice", backend.SilenceStateActive, 24*time.Hour),
		sil("soon", "bob", backend.SilenceStateActive, time.Hour),
		sil("mid", "carol", backend.SilenceStateActive, 6*time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences})
	require.Equal(t, "soon", p.view[0].s.ID)
	require.Equal(t, "mid", p.view[1].s.ID)
	require.Equal(t, "late", p.view[2].s.ID)
}

func TestPage_SortShortcutTogglesDirection(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	require.Equal(t, SortByEndsAt, p.sort)
	require.True(t, p.sortAsc)

	// Same column shortcut flips direction.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'E', Text: "E"})
	require.Equal(t, SortByEndsAt, p.sort)
	require.False(t, p.sortAsc)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'E', Text: "E"})
	require.True(t, p.sortAsc)

	// Different column resets to default direction.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C"})
	require.Equal(t, SortByCreatedBy, p.sort)
	require.True(t, p.sortAsc)
	// And then toggles on repeat.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C"})
	require.False(t, p.sortAsc)
}

func TestPage_SortByCreatedBy(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	silences := []backend.Silence{
		sil("a", "carol", backend.SilenceStateActive, time.Hour),
		sil("b", "alice", backend.SilenceStateActive, time.Hour),
		sil("c", "bob", backend.SilenceStateActive, time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'C', Text: "C", Mod: tea.ModShift})
	require.Equal(t, "alice", p.view[0].s.CreatedBy)
}

func TestPage_VimMotions(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	silences := []backend.Silence{
		sil("a", "x", backend.SilenceStateActive, time.Hour),
		sil("b", "y", backend.SilenceStateActive, 2*time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences})

	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.cursor)
}

func TestPage_AllWriteActionsAreDangerous(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	wantDangerous := map[string]bool{"n": true, "e": true, "x": true, "Ctrl+E": true, "Ctrl+X": true}
	for _, b := range p.Bindings() {
		if want, ok := wantDangerous[b.Key]; ok {
			require.True(t, b.Dangerous,
				"%s must carry Dangerous so read-only mode hides it (got %#v)", b.Key, b)
			require.True(t, want)
		}
	}
}

func TestPage_BulkExpireBindingIsBulk(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	for _, b := range p.Bindings() {
		if b.Key == "Ctrl+X" {
			require.True(t, b.Bulk,
				"Ctrl+X must be Bulk so the registry can no-op it without marks")
			return
		}
	}
	t.Fatal("Ctrl+X binding missing")
}

func TestPage_CtrlEFlashesEditorPlaceholder(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "$EDITOR")
}

func TestPage_CursorPreservedByID(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	first := []backend.Silence{
		sil("alpha", "x", backend.SilenceStateActive, time.Hour),
		sil("beta", "x", backend.SilenceStateActive, 2*time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: first})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "beta", p.view[p.cursor].s.ID)

	// "beta" now has a later ends-at; reordering pushes it to the
	// bottom. Cursor must follow.
	second := []backend.Silence{
		sil("gamma", "x", backend.SilenceStateActive, 30*time.Minute),
		sil("alpha", "x", backend.SilenceStateActive, time.Hour),
		sil("beta", "x", backend.SilenceStateActive, 4*time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: second})
	require.Equal(t, "beta", p.view[p.cursor].s.ID,
		"cursor must follow the focused silence by ID across refreshes")
}

func TestPage_EmptyState(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	out := stripStyle(p.View(80, 5))
	require.Contains(t, out, "no silences (yet)")
}

func TestPage_RenderShowsCreatorAndState(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	silences := []backend.Silence{
		sil("a", "alice@example", backend.SilenceStateActive, time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences})
	out := stripStyle(p.View(120, 10))
	require.Contains(t, out, "alice@example")
	require.Contains(t, out, "active")
}

func TestPage_FilterPromptIsLive(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	silences := []backend.Silence{
		sil("a", "alice@example", backend.SilenceStateActive, time.Hour),
		sil("b", "bob@example", backend.SilenceStateActive, 2*time.Hour),
		sil("c", "carol@example", backend.SilenceStateActive, 3*time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences})
	require.Len(t, p.view, 3)

	// Live filter applies on each keystroke without Enter.
	_, _ = p.Update(footer.PromptOpenedMsg{Mode: footer.PromptFilter})
	_, _ = p.Update(footer.PromptChangedMsg{Mode: footer.PromptFilter, Value: "alice"})
	require.Len(t, p.view, 1)
	require.Equal(t, "alice@example", p.view[0].s.CreatedBy)

	// Title carries the F/T count while a filter is on.
	require.Equal(t, "silences(all)[1/3]", p.Title())
	require.Equal(t, "filter:alice", p.HeaderContent())

	// Esc rolls back to the pre-prompt state (no filter).
	_, _ = p.Update(footer.PromptCancelledMsg{Mode: footer.PromptFilter})
	require.Empty(t, p.filter)
	require.Len(t, p.view, 3)
}

// fakeSilenceClient records every write call so tests can assert
// the silences page picked the right verb without a live backend.
// expireErr lets a test seed a per-call failure for partial-result
// flash assertions.
type fakeSilenceClient struct {
	created       backend.SilenceSpec
	updated       backend.SilenceSpec
	lastUpdateID  string
	expiredIDs    []string
	expireErr     error
	expireErrOnce map[string]error
}

func (f *fakeSilenceClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	f.created = spec
	return "fake-silence-id", nil
}

func (f *fakeSilenceClient) UpdateSilence(_ context.Context, id string, spec backend.SilenceSpec) error {
	f.updated = spec
	f.lastUpdateID = id
	return nil
}

func (f *fakeSilenceClient) ExpireSilence(_ context.Context, id string) error {
	if err, ok := f.expireErrOnce[id]; ok {
		return err
	}
	f.expiredIDs = append(f.expiredIDs, id)
	return f.expireErr
}

func TestPage_NewKeyWithoutClientsFlashesHint(t *testing.T) {
	t.Parallel()
	p := newPage(t) // no clients configured
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend",
		"`n` with no clients must explain rather than push a broken form")
}

func TestPage_NewKeyPushesFormWhenClientsAreConfigured(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]Client{"prod": &fakeSilenceClient{}},
		Creator: "wilfried",
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.NotNil(t, cmd, "n must produce a Cmd that pushes the form")
	// The Cmd carries a pushPageMsg internal to the app package; we
	// can only assert it isn't a flash (no Text field) and that the
	// type is the page-stack op.
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "n with clients must push the form, not flash")
}

// pageWithRows is a small helper that builds a silences page with
// a populated Clients map and feeds a poll.DataMsg of `count`
// silences tagged "prod" so cursor / mark / expire flows have
// something to operate on.
func pageWithRows(t *testing.T, fake *fakeSilenceClient, count int) *Page {
	t.Helper()
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]Client{"prod": fake},
		Creator: "wilfried",
	})
	silences := make([]backend.Silence, 0, count)
	for i := range count {
		silences = append(silences, backend.Silence{
			ID:        "sil-" + string(rune('a'+i)),
			CreatedBy: "alice",
			State:     backend.SilenceStateActive,
			StartsAt:  fixedNow.Add(-time.Hour),
			EndsAt:    fixedNow.Add(time.Hour * time.Duration(i+1)),
			Comment:   "ack",
			Matchers: []backend.Matcher{
				{Name: "alertname", Value: "HighCPU", IsEqual: true},
			},
		})
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences, Tenant: "prod"})
	return p
}

func TestPage_EditKeyOnEmptyViewFlashesHint(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]Client{"prod": &fakeSilenceClient{}},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no silence under the cursor")
}

func TestPage_EditKeyWithoutClientsFlashesHint(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	silences := []backend.Silence{sil("sil-1", "alice", backend.SilenceStateActive, time.Hour)}
	_, _ = p.Update(poll.DataMsg{Resource: silences, Tenant: "prod"})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no writeable backend")
}

func TestPage_EditKeyPushesEditForm(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "e with cursor + clients must push the form, not flash")
}

func TestPage_FormSubmittedUpdatedFlashesUpdated(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, cmd := p.Update(silenceform.SubmittedMsg{ID: "sil-7", Updated: true})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence updated: sil-7",
		"edit-mode submit must read \"updated\", not \"created\"")
}

func TestPage_ExpireKeyOnEmptyViewFlashesHint(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]Client{"prod": &fakeSilenceClient{}},
	})
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no silence under the cursor")
}

func TestPage_ExpireKeyOpensConfirmModal(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.NotNil(t, cmd)
	_, isFlash := cmd().(footer.FlashShowMsg)
	require.False(t, isFlash, "x must open a modal, not flash")
	// The pending-expire state must be loaded with the cursor row.
	require.Equal(t, []pendingExpireID{{id: "sil-a", tenant: "prod"}}, p.pendingExpire.ids)
	require.False(t, p.pendingExpire.bulk)
}

func TestPage_ConfirmYesCallsExpireSilence(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 1)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence expired")
	require.Equal(t, []string{"sil-a"}, fake.expiredIDs)
	require.Empty(t, p.pendingExpire.ids, "pending state must clear after confirm round")
}

func TestPage_ConfirmNoIsNoop(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 1)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: false})
	require.Nil(t, cmd)
	require.Empty(t, fake.expiredIDs)
}

func TestPage_ConfirmCancelledIsNoop(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 1)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := p.Update(modal.ConfirmResultMsg{Cancelled: true})
	require.Nil(t, cmd)
	require.Empty(t, fake.expiredIDs)
}

func TestPage_ConfirmFailureFlashesError(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{expireErr: errors.New("boom")}
	p := pageWithRows(t, fake, 1)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "expire failed")
}

func TestPage_SpaceTogglesMark(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 1)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Empty(t, p.marks, "second Space toggles the mark off")
}

func TestPage_BulkExpireRequiresMarks(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 2)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "no rows marked")
}

func TestPage_BulkExpireConfirmsAndIteratesMarks(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 2)
	// Mark both rows.
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 2)
	// Ctrl+X opens confirm.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	require.True(t, p.pendingExpire.bulk)
	require.Len(t, p.pendingExpire.ids, 2)
	// Yes → expire both, success flash.
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "expired 2 silences")
	require.ElementsMatch(t, []string{"sil-a", "sil-b"}, fake.expiredIDs)
	require.Empty(t, p.marks, "marks must clear after the bulk round resolves")
}

func TestPage_BulkExpireWalksByTenantNotView(t *testing.T) {
	t.Parallel()
	// Mark a row, then narrow the filter so the marked silence
	// drops out of the view. Ctrl+X must still queue and expire
	// it — marks live by ID across the filter, the user's intent
	// shouldn't be silently dropped by an unrelated UI state.
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace}) // mark sil-a
	require.Len(t, p.marks, 1)
	// Filter to "carol" — neither sample row's createdBy ("alice")
	// matches, so the view becomes empty while p.marks still
	// references sil-a.
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "carol"})
	require.Empty(t, p.view)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	require.Len(t, p.pendingExpire.ids, 1, "marks must drive the queue, not the live view")
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	require.NotNil(t, cmd)
	require.Equal(t, []string{"sil-a"}, fake.expiredIDs)
}

func TestPage_RenderShowsMarkGlyphOnMarkedRow(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // cursor → row 1
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})   // mark row 1
	out := stripStyle(p.View(120, 10))
	require.Contains(t, out, "✓",
		"marked row must render a visible mark glyph so the bulk-expire confirm has a row-level reference")
}

func TestPage_BulkExpireSummaryFlashesPartialFailure(t *testing.T) {
	t.Parallel()
	// Seed an error for sil-a only — sil-b succeeds.
	fake := &fakeSilenceClient{expireErrOnce: map[string]error{"sil-a": errors.New("boom")}}
	p := pageWithRows(t, fake, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	_, cmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "expired 1 of 2 — 1 failed")
}

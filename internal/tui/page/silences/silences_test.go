// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
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

func TestPage_TimeFormatToggleSwitchesEndsAndStartsColumns(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{sil("sil-1", "alice", backend.SilenceStateActive, time.Hour)},
		Tenant:   "",
	})

	out := stripStyle(p.View(160, 20))
	require.Contains(t, out, "1h ago",
		"relative mode renders StartsAt one hour earlier as `1h ago`")
	require.Contains(t, out, "1h",
		"relative mode renders EndsAt one hour later as `1h`")

	_, _ = p.Update(app.TimeFormatChangedMsg{Format: app.TimeFormatAbsolute})
	out = stripStyle(p.View(180, 20))
	require.Contains(t, out, "2026-",
		"absolute mode must surface the ISO local date prefix on both columns")
	// Per post-batch UX call, time mode is intentionally absent
	// from HeaderContent — the flash on `t` is the affordance,
	// and the visible cells make the mode self-evident.
	require.NotContains(t, p.HeaderContent(), "time:",
		"time mode must NOT take a HeaderContent slot — saves a body row")
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

func TestPage_FullPageMotions(t *testing.T) {
	t.Parallel()

	// Enough rows that Ctrl+F (20) lands inside the table without
	// clamping immediately.
	p := newPage(t)
	silences := make([]backend.Silence, 25)
	for i := range silences {
		silences[i] = sil("id"+string(rune('a'+i%26)), "creator", backend.SilenceStateActive, time.Hour)
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences})

	require.Equal(t, 0, p.cursor)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.cursor, "Ctrl+F is the full-page sibling of Ctrl+D")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+B mirrors Ctrl+F")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.cursor, "Ctrl+B clamps at 0; never goes negative")
}

func TestPage_AllWriteActionsAreDangerous(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	wantDangerous := map[string]bool{"n": true, "e": true, "x": true, "Ctrl+E": true}
	for _, b := range p.Bindings() {
		if want, ok := wantDangerous[b.Key]; ok {
			require.True(t, b.Dangerous,
				"%s must carry Dangerous so read-only mode hides it (got %#v)", b.Key, b)
			require.True(t, want)
		}
	}
}

func TestPage_CtrlXBindingRemoved(t *testing.T) {
	t.Parallel()

	// `x` is the single binding for both cursor-row expire and
	// bulk expire (k9s-style same-key-different-N). Ctrl+X must
	// not appear in Bindings — its presence would advertise a
	// shortcut that no longer routes anywhere and would clutter
	// the page hint strip.
	p := newPage(t)
	for _, b := range p.Bindings() {
		require.NotEqual(t, "Ctrl+X", b.Key, "Ctrl+X binding must be removed")
	}
}

func TestPage_CtrlEWithoutEditorFlashesHint(t *testing.T) {
	t.Parallel()
	// pageWithRows has a populated Clients map but no
	// EditorResolver — the zero-value resolver has no env keys
	// and no default editor, so Ctrl+E flashes a "configure
	// $EDITOR" hint instead of execing nothing.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	msg := cmd().(footer.FlashShowMsg)
	require.Contains(t, msg.Text, "$EDITOR")
}

// recordingResolver lets tests seed the editor's reply
// (content + err) without exec'ing a real binary or touching
// disk.
type recordingResolver struct {
	content string
	err     error
}

// editorPage builds a silences page with an editor.Resolver
// that synthesises a FinishedMsg from rec.content / rec.err so
// the test can drive the FinishedMsg branch deterministically.
func editorPage(t *testing.T, fake *fakeSilenceClient, rec *recordingResolver) *Page {
	t.Helper()
	p := New(Options{
		Styles:  loadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Clients: map[string]Client{"prod": fake},
		Creator: "wilfried",
		EditorResolver: edit.Resolver{
			DefaultEditor: "true", // satisfies "editor configured" guard
			ExecRunner: func(_ *exec.Cmd, _ func(error) tea.Msg) tea.Cmd {
				return func() tea.Msg {
					return edit.FinishedMsg{
						ResourceID: "sil-a",
						Content:    rec.content,
						Err:        rec.err,
					}
				}
			},
		},
	})
	silences := []backend.Silence{
		{
			ID:        "sil-a",
			CreatedBy: "alice",
			State:     backend.SilenceStateActive,
			StartsAt:  fixedNow.Add(-time.Hour),
			EndsAt:    fixedNow.Add(2 * time.Hour),
			Comment:   "ack",
			Matchers:  []backend.Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
		},
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences, Tenant: "prod"})
	return p
}

func TestPage_CtrlEEmitsEditCmd(t *testing.T) {
	t.Parallel()
	rec := &recordingResolver{}
	p := editorPage(t, &fakeSilenceClient{}, rec)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	require.NotNil(t, cmd, "Ctrl+E with editor configured must produce a Cmd")
	require.Equal(t, pendingEdit{id: "sil-a", tenant: "prod"}, p.pendingEdit,
		"pendingEdit must capture id + tenant at open time")
}

func TestPage_FinishedMsgEmptyContentIsNoop(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := editorPage(t, fake, &recordingResolver{})
	_, cmd := p.Update(edit.FinishedMsg{ResourceID: "sil-a", Content: ""})
	require.Nil(t, cmd, "empty content must be a silent no-op")
	require.Empty(t, fake.lastUpdateID, "no UpdateSilence call on empty content")
}

func TestPage_FinishedMsgErrorFlashes(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := editorPage(t, fake, &recordingResolver{})
	_, cmd := p.Update(edit.FinishedMsg{ResourceID: "sil-a", Err: errors.New("vim crashed")})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "vim crashed")
	require.Empty(t, fake.lastUpdateID)
}

func TestPage_FinishedMsgSuccessCallsUpdateSilence(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := editorPage(t, fake, &recordingResolver{})
	// Open the editor so pendingEdit is populated, then feed the
	// FinishedMsg with the round-tripped YAML.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	body, err := silenceToYAML(p.view[0].s)
	require.NoError(t, err)
	_, cmd := p.Update(edit.FinishedMsg{ResourceID: "sil-a", Content: string(body)})
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence updated: sil-a")
	require.Equal(t, "sil-a", fake.lastUpdateID)
}

func TestPage_FinishedMsgInvalidYAMLFlashes(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := editorPage(t, fake, &recordingResolver{})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	_, cmd := p.Update(edit.FinishedMsg{ResourceID: "sil-a", Content: "not: [valid"})
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "yaml")
	require.Empty(t, fake.lastUpdateID)
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

func TestPage_TitleColdStartReadsLoading(t *testing.T) {
	t.Parallel()

	// Pre-poll, the page's Title flips to "<spinner frame>
	// loading silences…" so the bordered body's top edge itself
	// reads as the loading affordance — k9s-style. The body
	// stays empty in this window so the spinner doesn't double
	// up.
	p := newPage(t)
	require.Contains(t, p.Title(), "loading silences…")
	require.NotContains(t, p.Title(), "silences(",
		"cold start title must not show a count form")
	body := stripStyle(p.View(80, 5))
	require.NotContains(t, body, "loading silences",
		"loading hint lives in the title now — body must not echo it")
	require.NotContains(t, body, "no silences",
		"cold start body must not claim there are no silences — we haven't asked yet")
}

func TestPage_TitleSwitchesBackAfterPoll(t *testing.T) {
	t.Parallel()

	// Once a DataMsg lands, the title returns to its count form
	// and the body picks up the appropriate "no silences (yet)"
	// copy when the answer is a true empty list.
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Silence{}, Tenant: "prod"})
	require.Equal(t, "silences(all)[0]", p.Title())
	body := stripStyle(p.View(80, 5))
	require.Contains(t, body, "no silences (yet)")
}

func TestPage_TitleFlipsToLoadingDuringRefresh(t *testing.T) {
	t.Parallel()

	// `r` re-enters the loading window — the title flips back to
	// "loading silences…" so the user sees the same affordance
	// they saw on cold start. The next DataMsg flips it back to
	// the count form.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	require.Equal(t, "silences(all)[1]", p.Title(),
		"populated page must read as a count title, not loading")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Contains(t, p.Title(), "loading silences…",
		"manual r must reuse the loading title affordance")
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{},
		Tenant:   "prod",
		At:       fixedNow,
		NextAt:   fixedNow.Add(30 * time.Second),
	})
	require.Equal(t, "silences(all)[0]", p.Title(),
		"DataMsg arrival must flip the title back to the count form")
}

func TestPage_OutOfScopeDataMsgDoesNotEndLoading(t *testing.T) {
	t.Parallel()

	// Regression: in a multi-backend setup with the scope narrowed
	// to one tenant, a fast out-of-scope tenant returning [] used
	// to flip the page into "polled, empty" state, briefly painting
	// "no silences (yet)" under a "silences(primary)[0]" title
	// while waiting for primary's first poll. Polled-ness must be
	// scope-aware: a staging report doesn't count when the scope
	// is "primary".
	p := newPage(t)
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "primary"})

	// Out-of-scope tenant answers first.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{},
		Tenant:   "staging",
		At:       fixedNow,
		NextAt:   fixedNow.Add(30 * time.Second),
	})
	require.Contains(t, p.Title(), "loading silences…",
		"out-of-scope DataMsg must not end the loading window")
	body := stripStyle(p.View(80, 5))
	require.NotContains(t, body, "no silences (yet)",
		"body must not flash the empty-list copy while the in-scope tenant is still pending")

	// In-scope tenant answers next — only now is the page polled.
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{
			{
				ID: "s-1", CreatedBy: "alice", State: backend.SilenceStateActive,
				StartsAt: fixedNow.Add(-time.Hour), EndsAt: fixedNow.Add(time.Hour),
			},
		},
		Tenant: "primary",
		At:     fixedNow,
		NextAt: fixedNow.Add(30 * time.Second),
	})
	require.Equal(t, "silences(primary)[1]", p.Title())
}

func TestPage_RefreshKeyEmitsRefreshRequestedAndKicksSpinner(t *testing.T) {
	t.Parallel()

	// `r` is the manual-refresh affordance: emits the typed App
	// message the wiring layer routes to the pollers, flips the
	// page into refreshing state, and re-kicks the spinner Tick
	// so the body's "refreshing…" hint animates while the nudge
	// is in flight.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	require.False(t, p.refreshing)

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.NotNil(t, cmd, "r must produce a Cmd")
	require.True(t, p.refreshing)

	// The Cmd is a tea.Batch — drain via cmd() once and inspect
	// the resulting BatchMsg.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok, "r must Batch the refresh and the spinner Tick")

	// Run each child Cmd to inspect the message it emits.
	seen := map[string]bool{}
	for _, child := range batch {
		if child == nil {
			continue
		}
		switch m := child().(type) {
		case app.RefreshRequestedMsg:
			seen["refresh"] = true
			require.Equal(t, "silences", m.Resource)
		case spinner.TickMsg:
			seen["tick"] = true
		}
	}
	require.True(t, seen["refresh"], "Batch must emit RefreshRequestedMsg")
	require.True(t, seen["tick"], "Batch must (re)kick the spinner Tick")
}

func TestPage_RefreshUsesActiveScope(t *testing.T) {
	t.Parallel()

	// The scope on the emitted RefreshRequestedMsg mirrors the
	// page's active scope so a `<1>` quick-switch followed by `r`
	// only nudges the picked tenant's poller — not every backend.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, _ = p.Update(app.ScopeChangedMsg{Scope: "prod"})

	_, cmd := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.NotNil(t, cmd)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	for _, child := range batch {
		if child == nil {
			continue
		}
		if m, ok := child().(app.RefreshRequestedMsg); ok {
			require.Equal(t, "prod", m.Scope)
			return
		}
	}
	t.Fatal("RefreshRequestedMsg never observed in the batch")
}

func TestPage_DataMsgClearsRefreshingFlag(t *testing.T) {
	t.Parallel()

	// The first DataMsg after `r` clears the refreshing flag so
	// the spinner stops and the static cadence subtitle takes
	// over. Without this, the spinner would loop forever after a
	// successful manual refresh.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.True(t, p.refreshing)

	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{},
		Tenant:   "prod",
		At:       fixedNow,
		NextAt:   fixedNow.Add(30 * time.Second),
	})
	require.False(t, p.refreshing,
		"DataMsg arrival must clear the in-flight refresh flag")
}

func TestPage_FooterShowsNextRefreshAfterPoll(t *testing.T) {
	t.Parallel()

	// Once the first DataMsg arrives carrying NextAt, the page's
	// Footer renders "next refresh 25s" — k9s-style symmetry with
	// the title in the top border. HeaderContent stays clean
	// (filter / mark / sort indicators only); cadence is ambient
	// frame state, not a subtitle.
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{},
		Tenant:   "prod",
		At:       fixedNow.Add(-5 * time.Second),
		NextAt:   fixedNow.Add(25 * time.Second),
	})
	require.Equal(t, "next refresh 25s", p.Footer())
	require.NotContains(t, p.HeaderContent(), "next",
		"cadence must not leak into the header subtitle once it lives in Footer")
	require.NotContains(t, p.HeaderContent(), "last",
		"the dropped \"last X ago\" segment must not resurrect in the header")
}

func TestPage_FooterRefreshingOverridesCadence(t *testing.T) {
	t.Parallel()

	// Between `r` and the next DataMsg, Footer reads "refreshing
	// …" so the user has direct frame-level feedback the nudge
	// landed. The static "next refresh" copy is suppressed in
	// this window — it's stale until the new DataMsg updates the
	// NextAt timestamp.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{},
		Tenant:   "prod",
		At:       fixedNow.Add(-5 * time.Second),
		NextAt:   fixedNow.Add(25 * time.Second),
	})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Equal(t, "refreshing…", p.Footer())
}

func TestPage_FooterEmptyPrePoll(t *testing.T) {
	t.Parallel()
	// Pre-poll the bottom border stays a plain rule — the spinner
	// in the body already says "loading", and a "next refresh"
	// label here would be a lie because we have no NextAt yet.
	p := newPage(t)
	require.Empty(t, p.Footer())
}

func TestPage_NextRefreshLabelEdgeCases(t *testing.T) {
	t.Parallel()

	// Past-due reads "due"; sub-second reads "<1s"; minute / hour
	// boundaries truncate. The subtitle is the user's only signal
	// when the next pull will land — wording stability matters.
	now := fixedNow
	cases := []struct {
		name string
		next time.Time
		want string
	}{
		{"due-now", now, "due"},
		{"past", now.Add(-time.Second), "due"},
		{"sub-second", now.Add(500 * time.Millisecond), "<1s"},
		{"seconds", now.Add(25 * time.Second), "25s"},
		{"minutes", now.Add(3 * time.Minute), "3m"},
		{"hours", now.Add(2 * time.Hour), "2h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, nextRefreshLabel(now, tc.next))
		})
	}
}

func TestPage_ExpiredSilenceIsDimmed(t *testing.T) {
	t.Parallel()

	// Expired silences read like the alerts page's suppressed
	// alerts: foreground-only dim so the row is still legible
	// but visibly demoted. The active row in the same view
	// stays at full contrast so the comparison is obvious.
	styles := loadStyles(t)
	p := New(Options{
		Styles: styles,
		Now:    func() time.Time { return fixedNow },
	})
	_, _ = p.Update(poll.DataMsg{
		Resource: []backend.Silence{
			{
				ID:        "live",
				CreatedBy: "alice",
				State:     backend.SilenceStateActive,
				StartsAt:  fixedNow.Add(-time.Hour),
				EndsAt:    fixedNow.Add(time.Hour),
			},
			{
				ID:        "dead",
				CreatedBy: "bob",
				State:     backend.SilenceStateExpired,
				StartsAt:  fixedNow.Add(-2 * time.Hour),
				EndsAt:    fixedNow.Add(-time.Hour),
			},
		},
		Tenant: "prod",
	})

	// Default sort is EndsAt-asc, so the expired (older EndsAt)
	// row lands at index 0 and inherits the cursor band. Move the
	// cursor down so the cursor styling no longer shadows the
	// expired row's dim — the test isolates the expired-dim
	// treatment by asserting on the row that's NOT under the
	// cursor.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	out := p.View(120, 6)
	lines := strings.Split(out, "\n")
	var expiredLine, activeLine string
	for _, line := range lines {
		stripped := stripStyle(line)
		switch {
		case strings.Contains(stripped, "bob"):
			expiredLine = line
		case strings.Contains(stripped, "alice"):
			activeLine = line
		}
	}
	require.NotEmpty(t, expiredLine, "expired row missing from output")
	require.NotEmpty(t, activeLine, "active row missing from output")
	dimFG, _, _, _ := styles.Table.Dimmed.GetForeground().RGBA()
	require.Contains(t, expiredLine, hexEscapeFor(styles.Table.Dimmed.GetForeground()),
		"expired row must carry the dimmed fg colour (%08x)", dimFG)
}

// visualColumnOf returns the display-width column at which token
// first appears in line. Display width (lipgloss.Width) rather
// than byte index because some prefix glyphs (▸) are multi-byte
// but one visual cell wide — using strings.Index would compare
// apples to oranges.
func visualColumnOf(t *testing.T, line, token string) int {
	t.Helper()
	idx := strings.Index(line, token)
	require.GreaterOrEqual(t, idx, 0, "token %q missing from line %q", token, line)
	return lipgloss.Width(line[:idx])
}

func hexEscapeFor(c color.Color) string {
	r, g, b, _ := c.RGBA()
	// lipgloss emits truecolor as ESC[38;2;R;G;Bm — match the RGB
	// triple verbatim so the assertion doesn't need the full ANSI
	// reset/leading sequence.
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
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

// TestPage_HeaderColumnsAlignWithRows guards the alignment fix:
// the row prefix is always two cols (cursor "▸ " / "  ") plus
// optionally two more for the mark glyph, and the header must
// reserve the same leading space so column titles line up with
// their data.
func TestPage_HeaderColumnsAlignWithRows(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mark bool
	}{
		{name: "no marks", mark: false},
		{name: "with marks", mark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := newPage(t)
			// EndsAt 30m in the past, StartsAt 5h in the past (sil()
			// always sets StartsAt = fixedNow - 1h, so widen it via
			// a manual override). Distinct relative-time strings —
			// "30m ago" vs. "5h ago" — keep the column-search below
			// unambiguous. Uses an expired silence because FormatAge
			// collapses every future timestamp to "now".
			s := sil("only", "alice", backend.SilenceStateExpired, -30*time.Minute)
			s.StartsAt = fixedNow.Add(-5 * time.Hour)
			_, _ = p.Update(poll.DataMsg{Resource: []backend.Silence{s}})
			if tc.mark {
				_, _ = p.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
			}
			out := stripStyle(p.View(160, 5))
			lines := strings.Split(out, "\n")
			require.GreaterOrEqual(t, len(lines), 2,
				"need a header line and at least one data row")
			header, data := lines[0], lines[1]

			// ENDS header and its payload (30m ago) must start at the
			// same visual column — otherwise the table reads
			// shifted, like in the bug report screenshot. Compared
			// in display widths (lipgloss.Width) because the ▸
			// cursor glyph is multi-byte but one visual cell.
			hdrCol := visualColumnOf(t, header, "ENDS")
			rowCol := visualColumnOf(t, data, "30m")
			require.Equal(t, hdrCol, rowCol,
				"ENDS column header and data must start in the same visual column (header=%q row=%q)", header, data)
		})
	}
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
// flash assertions. Concurrent-safe because the bulk-expire fanout
// drives ExpireSilence from multiple goroutines.
type fakeSilenceClient struct {
	mu            sync.Mutex
	created       backend.SilenceSpec
	updated       backend.SilenceSpec
	lastUpdateID  string
	expiredIDs    []string
	expireErr     error
	expireErrOnce map[string]error
}

func (f *fakeSilenceClient) CreateSilence(_ context.Context, spec backend.SilenceSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = spec
	return "fake-silence-id", nil
}

func (f *fakeSilenceClient) UpdateSilence(_ context.Context, id string, spec backend.SilenceSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = spec
	f.lastUpdateID = id
	return nil
}

func (f *fakeSilenceClient) ExpireSilence(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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

	flashCmd := runBulk(t, p)
	msg := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "silence expired",
		"single-row total of 1 must use the singular 'silence expired' wording")
	fake.mu.Lock()
	require.Equal(t, []string{"sil-a"}, fake.expiredIDs)
	fake.mu.Unlock()
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

	flashCmd := runBulk(t, p)
	msg := flashCmd().(footer.FlashShowMsg)
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

func TestPage_XKeyNoMarksUsesCursor(t *testing.T) {
	t.Parallel()

	// No marks → `x` falls through to the cursor-row path and
	// opens a single-row confirm. Wording mirrors the legacy
	// single-row flow ("expire silence sil-a?"); pendingExpire
	// captures one id with bulk=false so the partial-failure
	// flash reads correctly downstream.
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 2)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.NotNil(t, cmd)
	require.False(t, p.pendingExpire.bulk, "no marks → cursor-row path, bulk=false")
	require.Len(t, p.pendingExpire.ids, 1)
	require.Equal(t, "sil-a", p.pendingExpire.ids[0].id)
	// The Cmd opens the confirm modal — flash assertion would be
	// the wrong shape here. Cmd not nil is the contract.
}

func TestPage_XKeyWithMarksGoesBulk(t *testing.T) {
	t.Parallel()

	// Marks → `x` queues every marked silence with bulk=true.
	// Wording for ≥2 marks adds the tenant breakdown; for the
	// single-tenant case the breakdown is just the tenant name.
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 2)
	// Mark both rows.
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 2)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.NotNil(t, cmd, "marks present → x must open the bulk confirm")
	require.True(t, p.pendingExpire.bulk)
	require.Len(t, p.pendingExpire.ids, 2)
}

// runBulk drives a confirmed bulk-expire fanout to completion and
// returns the flash command Update emits at the end. The fanout
// dispatches to an internal goroutine and returns a Cmd that
// blocks on completion; tests run that Cmd inline so the result
// is deterministic.
func runBulk(t *testing.T, p *Page) tea.Cmd {
	t.Helper()
	_, dispatchCmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	require.NotNil(t, dispatchCmd, "Yes must produce the bulk-expire dispatch Cmd")
	doneMsg := dispatchCmd()
	done, ok := doneMsg.(bulkExpireDoneMsg)
	require.True(t, ok, "dispatch Cmd must emit bulkExpireDoneMsg, got %T", doneMsg)
	_, flashCmd := p.Update(done)
	return flashCmd
}

func TestPage_BulkExpireConfirmsAndIteratesMarks(t *testing.T) {
	t.Parallel()
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.True(t, p.pendingExpire.bulk)
	require.Len(t, p.pendingExpire.ids, 2)

	flashCmd := runBulk(t, p)
	msg := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashSuccess, msg.Level)
	require.Contains(t, msg.Text, "expired 2 silences")
	require.ElementsMatch(t, []string{"sil-a", "sil-b"}, fake.expiredIDs)
	require.Empty(t, p.marks, "every successful expire must drop its mark")
}

func TestPage_BulkExpireWalksByTenantNotView(t *testing.T) {
	t.Parallel()

	// Mark a row, then narrow the filter so the marked silence
	// drops out of the view. `x` must still queue and expire it —
	// marks live by ID across the filter, the user's intent
	// shouldn't be silently dropped by an unrelated UI state.
	fake := &fakeSilenceClient{}
	p := pageWithRows(t, fake, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace}) // mark sil-a
	require.Len(t, p.marks, 1)
	_, _ = p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "carol"})
	require.Empty(t, p.view)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Len(t, p.pendingExpire.ids, 1, "marks must drive the queue, not the live view")
	flashCmd := runBulk(t, p)
	require.NotNil(t, flashCmd)
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

	// Seed an error for sil-a only — sil-b succeeds. After the
	// fanout resolves the partial flash reads "expired 1 of 2 —
	// 1 failed", and the failed silence keeps its mark so the
	// user can retry only the unfinished work.
	fake := &fakeSilenceClient{expireErrOnce: map[string]error{"sil-a": errors.New("boom")}}
	p := pageWithRows(t, fake, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	flashCmd := runBulk(t, p)
	msg := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashWarn, msg.Level)
	require.Contains(t, msg.Text, "expired 1 of 2 — 1 failed")
	require.Contains(t, p.marks, "sil-a", "failed mark must survive for retry")
	require.NotContains(t, p.marks, "sil-b", "successful mark must clear")
}

func TestPage_BulkExpireSummaryFlashesTotalFailure(t *testing.T) {
	t.Parallel()

	// Every queued silence's ExpireSilence returns an error. Flash
	// reads "expire failed for N silences" at FlashError level.
	// Failed marks all survive so re-pressing `x` retries the lot.
	fake := &fakeSilenceClient{expireErr: errors.New("boom")}
	p := pageWithRows(t, fake, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	flashCmd := runBulk(t, p)
	msg := flashCmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashError, msg.Level)
	require.Contains(t, msg.Text, "expire failed for 2 silences")
	require.Len(t, p.marks, 2, "every failed mark must survive for retry")
}

func TestPage_BulkExpireUnmarksOnlySuccessfulIDs(t *testing.T) {
	t.Parallel()

	// Three marks, the middle one fails. The two successes drop
	// their marks; the failure keeps its mark so re-pressing `x`
	// retries only the unfinished work. Mirrors the alerts-side
	// rule once that lands; pinning here makes the unmark contract
	// load-bearing for both pages.
	fake := &fakeSilenceClient{expireErrOnce: map[string]error{"sil-b": errors.New("boom")}}
	p := pageWithRows(t, fake, 3)
	for range 3 {
		_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Len(t, p.marks, 3)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	_ = runBulk(t, p)
	require.Len(t, p.marks, 1, "only failures keep marks")
	require.Contains(t, p.marks, "sil-b")
}

func TestPage_BulkExpireRespectsConcurrency(t *testing.T) {
	t.Parallel()

	// Build a fake whose ExpireSilence blocks until release is
	// closed, recording the high-watermark of in-flight callers.
	// Concurrency = 2 with 5 marks → at most 2 blocked at once.
	fake := newConcurrencyFake()
	p := New(Options{
		Styles:          loadStyles(t),
		Now:             func() time.Time { return fixedNow },
		Clients:         map[string]Client{"prod": fake},
		Creator:         "wilfried",
		BulkConcurrency: 2,
	})
	silences := make([]backend.Silence, 0, 5)
	for i := range 5 {
		silences = append(silences, backend.Silence{
			ID:        "sil-" + string(rune('a'+i)),
			CreatedBy: "alice",
			State:     backend.SilenceStateActive,
			StartsAt:  fixedNow.Add(-time.Hour),
			EndsAt:    fixedNow.Add(time.Hour),
		})
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences, Tenant: "prod"})
	for range 5 {
		_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Len(t, p.marks, 5)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	// Drive the dispatcher in a goroutine; it blocks until
	// every call returns. We release them one at a time and
	// observe the in-flight count never exceeds the concurrency
	// limit.
	_, dispatchCmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	resultCh := make(chan tea.Msg, 1)
	go func() { resultCh <- dispatchCmd() }()

	// Let all five callers race to acquire the gate; with
	// concurrency=2, only two should be in flight at any moment.
	// Wait until at least two are blocked, then release one at a
	// time and assert the watermark stayed at 2.
	require.Eventually(t, func() bool { return fake.inFlight() >= 2 }, time.Second, time.Millisecond)
	for range 5 {
		fake.release()
	}
	<-resultCh
	require.LessOrEqual(t, fake.peak(), 2,
		"concurrency=2 must cap in-flight callers per tenant; got peak %d", fake.peak())
}

func TestPage_BulkExpireCancelsOnPageClose(t *testing.T) {
	t.Parallel()

	// Page.Close mid-fanout must cancel pending workers so they
	// exit without dispatching the rest. Five marks, concurrency=1
	// (sequential) → release the first call, Close, observe the
	// dispatcher still drains (in-flight finishes) but no further
	// callers arrive at the fake after Close.
	fake := newConcurrencyFake()
	p := New(Options{
		Styles:          loadStyles(t),
		Now:             func() time.Time { return fixedNow },
		Clients:         map[string]Client{"prod": fake},
		Creator:         "wilfried",
		BulkConcurrency: 1,
	})
	silences := make([]backend.Silence, 0, 5)
	for i := range 5 {
		silences = append(silences, backend.Silence{
			ID:        "sil-" + string(rune('a'+i)),
			CreatedBy: "alice",
			State:     backend.SilenceStateActive,
			StartsAt:  fixedNow.Add(-time.Hour),
			EndsAt:    fixedNow.Add(time.Hour),
		})
	}
	_, _ = p.Update(poll.DataMsg{Resource: silences, Tenant: "prod"})
	for range 5 {
		_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	_, _ = p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	_, dispatchCmd := p.Update(modal.ConfirmResultMsg{Yes: true})
	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- dispatchCmd() }()

	require.Eventually(t, func() bool { return fake.inFlight() >= 1 }, time.Second, time.Millisecond)
	_ = p.Close()
	fake.release() // unblock the in-flight call so it can finish
	// The producer goroutine sees ctx.Done() and stops feeding
	// jobs; no further callers should arrive at the fake.
	doneMsg := <-doneCh
	done := doneMsg.(bulkExpireDoneMsg)
	require.Less(t, len(done.successes), 5,
		"Close must short-circuit the fanout — got %d successes", len(done.successes))
	require.LessOrEqual(t, fake.totalCalls(), 1,
		"after Close the dispatcher must not start additional ExpireSilence calls; got %d", fake.totalCalls())
}

func TestPage_BulkExpireDoneDoesNotCancelLatestRound(t *testing.T) {
	t.Parallel()

	// Pin the cancel-by-identity contract: a stale bulkExpireDoneMsg
	// arriving on Update must not abort an in-flight newer round.
	// The Cmd that produced the message already deferred its own
	// cancel(); handleBulkExpireDone is forbidden from touching
	// p.cancelBulk because that field now points to the *newer*
	// round.
	//
	// Set up a non-nil p.cancelBulk via a stub round, then deliver
	// a stale bulkExpireDoneMsg to Update and assert p.cancelBulk
	// is still non-nil and uncancelled afterwards.
	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.cancelBulk = cancel
	ctxBefore := p.cancelBulk

	_, _ = p.Update(bulkExpireDoneMsg{bulk: false, total: 1, successes: []string{"sil-a"}})

	require.NotNil(t, p.cancelBulk, "stale done must not nil out the latest round's cancel")
	// Pointer identity: same cancel func still installed.
	require.Equal(t,
		fmt.Sprintf("%p", ctxBefore),
		fmt.Sprintf("%p", p.cancelBulk),
		"the cancel func must be the same instance — handleBulkExpireDone may not touch p.cancelBulk")
}

// concurrencyFake is a controllable Client used by the
// concurrency / cancellation tests. ExpireSilence blocks on a
// per-call gate so the test can observe how many callers are
// in flight at once and release them in a controlled order.
type concurrencyFake struct {
	mu       sync.Mutex
	inflight int
	peakIn   int
	calls    int
	gate     chan struct{}
}

func newConcurrencyFake() *concurrencyFake {
	return &concurrencyFake{gate: make(chan struct{}, 256)}
}

func (f *concurrencyFake) CreateSilence(context.Context, backend.SilenceSpec) (string, error) {
	return "", nil
}

func (f *concurrencyFake) UpdateSilence(context.Context, string, backend.SilenceSpec) error {
	return nil
}

func (f *concurrencyFake) ExpireSilence(_ context.Context, _ string) error {
	f.mu.Lock()
	f.calls++
	f.inflight++
	if f.inflight > f.peakIn {
		f.peakIn = f.inflight
	}
	f.mu.Unlock()
	<-f.gate
	f.mu.Lock()
	f.inflight--
	f.mu.Unlock()
	return nil
}

func (f *concurrencyFake) release() { f.gate <- struct{}{} }

func (f *concurrencyFake) inFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inflight
}

func (f *concurrencyFake) peak() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peakIn
}

func (f *concurrencyFake) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestPage_ClearMarksMsgEmptiesMarks(t *testing.T) {
	t.Parallel()

	// Ctrl+\ at LayerGlobal lands as ClearMarksMsg. With marks
	// active the silences page empties the map and flashes
	// "marks cleared" so the user sees the affordance fired.
	p := pageWithRows(t, &fakeSilenceClient{}, 2)
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.Len(t, p.marks, 2)

	_, cmd := p.Update(app.ClearMarksMsg{})
	require.Empty(t, p.marks, "ClearMarksMsg must drop every mark")
	require.NotNil(t, cmd)
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Contains(t, msg.Text, "marks cleared")
}

func TestPage_ClearMarksMsgWithNoMarksIsSilent(t *testing.T) {
	t.Parallel()

	p := pageWithRows(t, &fakeSilenceClient{}, 1)
	require.Empty(t, p.marks)

	_, cmd := p.Update(app.ClearMarksMsg{})
	require.Empty(t, p.marks)
	require.Nil(t, cmd, "no-marks ClearMarksMsg must not flash")
}

func TestPage_FormatTenantBreakdown(t *testing.T) {
	t.Parallel()

	// Single tenant → bare name; multi tenant → comma-joined
	// "name=count" sorted alphabetically. Pinning sort order so
	// the confirm modal wording is deterministic across runs.
	cases := []struct {
		name string
		ids  []pendingExpireID
		want string
	}{
		{
			name: "single tenant",
			ids:  []pendingExpireID{{id: "a", tenant: "prod"}, {id: "b", tenant: "prod"}},
			want: "prod",
		},
		{
			name: "multi tenant sorted",
			ids: []pendingExpireID{
				{id: "a", tenant: "staging"},
				{id: "b", tenant: "prod"},
				{id: "c", tenant: "prod"},
			},
			want: "prod=2, staging=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, formatTenantBreakdown(tc.ids))
		})
	}
}

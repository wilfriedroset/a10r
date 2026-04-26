// SPDX-License-Identifier: Apache-2.0

package silences

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
	return New(loadStyles(t), func() time.Time { return fixedNow })
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
	require.Equal(t, "soon", p.view[0].ID)
	require.Equal(t, "mid", p.view[1].ID)
	require.Equal(t, "late", p.view[2].ID)
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
	require.Equal(t, "alice", p.view[0].CreatedBy)
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

func TestPage_ActionKeysFlashPlaceholders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key  rune
		want string
	}{
		{key: 'n', want: "silence form"},
		{key: 'e', want: "silence edit"},
		{key: 'x', want: "silence expire"},
	}
	for _, tc := range cases {
		t.Run(string(tc.key), func(t *testing.T) {
			t.Parallel()
			p := newPage(t)
			_, cmd := p.Update(tea.KeyPressMsg{Code: tc.key, Text: string(tc.key)})
			require.NotNil(t, cmd)
			msg := cmd().(footer.FlashShowMsg)
			require.Contains(t, msg.Text, tc.want)
			require.Equal(t, footer.FlashWarn, msg.Level)
		})
	}
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
	require.Equal(t, "beta", p.view[p.cursor].ID)

	// "beta" now has a later ends-at; reordering pushes it to the
	// bottom. Cursor must follow.
	second := []backend.Silence{
		sil("gamma", "x", backend.SilenceStateActive, 30*time.Minute),
		sil("alpha", "x", backend.SilenceStateActive, time.Hour),
		sil("beta", "x", backend.SilenceStateActive, 4*time.Hour),
	}
	_, _ = p.Update(poll.DataMsg{Resource: second})
	require.Equal(t, "beta", p.view[p.cursor].ID,
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

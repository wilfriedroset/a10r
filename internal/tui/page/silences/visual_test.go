// SPDX-License-Identifier: Apache-2.0

package silences

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

var (
	keyVisual = tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift}
	keyDown   = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keySpace  = tea.KeyPressMsg{Code: ' ', Text: " "}
)

func markedIDs(p *Page) []string {
	out := make([]string, 0, len(p.marks))
	for k := range p.marks {
		out = append(out, k)
	}
	return out
}

func TestVisual_SpaceCommitsTheRange(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyVisual)
	require.True(t, p.Visual.On())
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keySpace)

	require.False(t, p.Visual.On(), "a commit leaves visual mode")
	require.ElementsMatch(t, []string{"sil-a", "sil-b", "sil-c"}, markedIDs(p))
}

func TestVisual_SecondVCommitsTheRange(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyVisual)

	require.False(t, p.Visual.On())
	require.ElementsMatch(t, []string{"sil-a", "sil-b"}, markedIDs(p))
}

func TestVisual_CommitKeepsMarksOutsideTheRange(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keySpace) // mark sil-d on its own
	for range 3 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keySpace)

	require.ElementsMatch(t, []string{"sil-a", "sil-b", "sil-d"}, markedIDs(p))
}

func TestVisual_EscCancelsWithoutMarking(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	require.True(t, p.ConsumeEscape(), "Esc must unwind the range before it pops the page")
	require.False(t, p.Visual.On())
	require.Empty(t, markedIDs(p), "a cancelled range marks nothing")
	require.False(t, p.ConsumeEscape(), "a second Esc belongs to the global pop")
}

func TestVisual_ClearMarksCancelsTheRange(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keySpace)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	cmd := p.handleClearMarks()

	require.False(t, p.Visual.On(), "Ctrl+\\ drops the open range with the marks")
	require.Empty(t, markedIDs(p))
	require.NotNil(t, cmd)
}

func TestVisual_LostAnchorCancelsWithFlash(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyVisual)
	_, cmd := p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "nothing-matches-this"})

	require.False(t, p.Visual.On())
	require.NotNil(t, cmd, "a dropped anchor must state its reason")
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Equal(t, listpage.HintVisualCancelled, msg.Text)
}

func TestVisual_ExpireCommitsTheRangeFirst(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	require.NotNil(t, cmd)
	require.False(t, p.Visual.On())
	require.True(t, p.pendingExpire.bulk, "an open range reaches `x` as marks")
	require.Len(t, p.pendingExpire.ids, 2)
}

func TestVisual_HeaderChipAndBinding(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	require.NotContains(t, p.HeaderContent(), listpage.ChipVisual)
	_, _ = p.Update(keyVisual)
	require.Contains(t, p.HeaderContent(), listpage.ChipVisual)

	for _, b := range p.Bindings() {
		if b.Key == "Shift+V" {
			require.True(t, b.Shared, "range marking is shared, like Space")
			require.False(t, b.Dangerous, "marking touches no remote state")
			return
		}
	}
	t.Fatal("Bindings() must advertise Shift+V")
}

func TestVisual_EmptyViewDoesNotAnchor(t *testing.T) {
	t.Parallel()
	p := newPage(t)
	_, _ = p.Update(keyVisual)
	require.False(t, p.Visual.On(), "there is no row to anchor on")
}

func TestVisual_PreviewRendersLikeAMark(t *testing.T) {
	t.Parallel()
	p := pageWithRows(t, &fakeSilenceClient{}, 4)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)

	require.Equal(t, 2, strings.Count(p.View(140, 24), "\u2713"),
		"both rows of the range must preview with the mark glyph")
}

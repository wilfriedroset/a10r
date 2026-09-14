// SPDX-License-Identifier: Apache-2.0

package groupdetail

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
)

var (
	keyVisual = tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift}
	keyDown   = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keySpace  = tea.KeyPressMsg{Code: ' ', Text: " "}
)

// visualPage seeds four same-severity instances so the sort is a
// stable web-0..web-3 and every range index in the tests is explicit.
func visualPage(t *testing.T) *Page {
	t.Helper()
	insts := make([]backend.Alert, 4)
	for i := range insts {
		insts[i] = instance(fmt.Sprintf("fp-%d", i), "warning", backend.AlertStateActive,
			map[string]string{sortKeyInstance: fmt.Sprintf("web-%d", i)})
	}
	return newWritablePage(t, insts...)
}

func markedFingerprints(p *Page) []string {
	out := make([]string, 0, len(p.marks))
	for k := range p.marks {
		out = append(out, k)
	}
	return out
}

func TestVisual_SpaceCommitsTheRange(t *testing.T) {
	t.Parallel()
	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	require.True(t, p.Visual.On())
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keySpace)

	require.False(t, p.Visual.On(), "a commit leaves visual mode")
	require.ElementsMatch(t, []string{"fp-0", "fp-1", "fp-2"}, markedFingerprints(p))
}

func TestVisual_EscCancelsWithoutMarking(t *testing.T) {
	t.Parallel()
	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	require.True(t, p.ConsumeEscape(), "Esc must unwind the range before it pops the page")
	require.Empty(t, markedFingerprints(p), "a cancelled range marks nothing")
	require.False(t, p.ConsumeEscape(), "a second Esc belongs to the global pop")
}

func TestVisual_ClearMarksCancelsTheRange(t *testing.T) {
	t.Parallel()
	p := visualPage(t)
	_, _ = p.Update(keySpace)
	_, _ = p.Update(keyVisual)
	require.NotNil(t, p.handleClearMarks())
	require.False(t, p.Visual.On(), "Ctrl+\\ drops the open range with the marks")
	require.Empty(t, markedFingerprints(p))
}

func TestVisual_LostAnchorCancelsWithFlash(t *testing.T) {
	t.Parallel()
	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	_, cmd := p.Update(footer.PromptSubmittedMsg{Mode: footer.PromptFilter, Value: "nothing-matches-this"})

	require.False(t, p.Visual.On())
	require.NotNil(t, cmd, "a dropped anchor must state its reason")
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, footer.FlashInfo, msg.Level)
	require.Equal(t, listpage.HintVisualCancelled, msg.Text)
}

func TestVisual_SilenceCommitsTheRangeFirst(t *testing.T) {
	t.Parallel()
	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	_, cmd := p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	require.NotNil(t, cmd)
	require.False(t, p.Visual.On())
	require.Len(t, p.pendingBulkSilence.targets, 2, "an open range reaches `s` as marks")
}

func TestVisual_HeaderChipAndBinding(t *testing.T) {
	t.Parallel()
	p := visualPage(t)
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
	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)

	require.Equal(t, 2, strings.Count(p.View(140, 24), "✓"),
		"both rows of the range must preview with the mark glyph")
}

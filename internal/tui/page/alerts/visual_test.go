// SPDX-License-Identifier: Apache-2.0

package alerts

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	silenceform "github.com/wilfriedroset/a10r/internal/tui/form/silence"
	"github.com/wilfriedroset/a10r/internal/tui/page/listpage"
	"github.com/wilfriedroset/a10r/internal/tui/poll"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var (
	keyVisual = tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift}
	keyDown   = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keySpace  = tea.KeyPressMsg{Code: ' ', Text: " "}
)

// visualPage seeds four same-severity groups and sorts by alertname
// ASC, so the view reads A, B, C, D and the range indexes in each
// test are predictable.
func visualPage(t *testing.T) *Page {
	t.Helper()
	p := newPage(t)
	_, _ = p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("A", "warning", backend.AlertStateActive, "f1", time.Minute, nil),
		mkAlert("B", "warning", backend.AlertStateActive, "f2", time.Minute, nil),
		mkAlert("C", "warning", backend.AlertStateActive, "f3", time.Minute, nil),
		mkAlert("D", "warning", backend.AlertStateActive, "f4", time.Minute, nil),
	}})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'N', Text: "N", Mod: tea.ModShift})
	require.Equal(t, []string{"A", "B", "C", "D"}, viewNames(p))
	return p
}

// viewNames returns the alertnames in render order.
func viewNames(p *Page) []string {
	out := make([]string, 0, len(p.groups))
	for _, g := range p.groups {
		out = append(out, g.alertName)
	}
	return out
}

// markedNames returns the alertnames of the marked groups.
func markedNames(p *Page) []string {
	out := make([]string, 0, len(p.marks))
	for _, g := range p.groups {
		if _, ok := p.marks[g.key()]; ok {
			out = append(out, g.alertName)
		}
	}
	return out
}

func TestVisual_SpaceCommitsTheRange(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	require.True(t, p.Visual.On())
	require.Contains(t, p.HeaderContent(), listpage.ChipVisual)

	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keySpace)

	require.Equal(t, []string{"A", "B", "C"}, markedNames(p))
	require.False(t, p.Visual.On(), "commit leaves visual mode")
	require.NotContains(t, p.HeaderContent(), listpage.ChipVisual)
}

func TestVisual_SecondVCommitsTheRange(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyVisual)

	require.Equal(t, []string{"A", "B"}, markedNames(p))
	require.False(t, p.Visual.On())
}

func TestVisual_CommitKeepsMarksOutsideTheRange(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keySpace) // D marked one by one
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	_, _ = p.Update(keySpace) // range A..B, cursor above the anchor

	require.Equal(t, []string{"A", "B", "D"}, markedNames(p))
}

func TestVisual_EscCancelsWithoutMarking(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)

	require.True(t, p.ConsumeEscape(), "Esc is taken by the open range, not by the page pop")
	require.False(t, p.Visual.On())
	require.Empty(t, p.marks)
	require.False(t, p.ConsumeEscape(), "the next Esc pops the page as usual")
}

func TestVisual_ClearMarksCancelsTheRange(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	_, _ = p.Update(keySpace)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(keyVisual)

	_, _ = p.Update(app.ClearMarksMsg{})
	require.Empty(t, p.marks)
	require.False(t, p.Visual.On(), "Ctrl+\\ drops the range along with the marks")
}

func TestVisual_LostAnchorCancelsWithFlash(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	_, _ = p.Update(keyVisual)
	require.True(t, p.Visual.On())

	// The anchored row (A) leaves the view on the next poll.
	_, cmd := p.Update(poll.DataMsg{Resource: []backend.Alert{
		mkAlert("B", "warning", backend.AlertStateActive, "f2", time.Minute, nil),
		mkAlert("C", "warning", backend.AlertStateActive, "f3", time.Minute, nil),
	}})

	require.False(t, p.Visual.On())
	require.NotNil(t, cmd, "the user needs a reason for the vanished preview")
	msg := cmd().(footer.FlashShowMsg)
	require.Equal(t, listpage.HintVisualCancelled, msg.Text)
}

func TestVisual_SilenceCommitsTheRangeFirst(t *testing.T) {
	t.Parallel()

	p := visualPage(t)
	p.clients = map[string]silenceform.Client{"": &fakeSilenceClient{}}
	_, _ = p.Update(keyVisual)
	_, _ = p.Update(keyDown)
	_, _ = p.Update(tea.KeyPressMsg{Code: 's', Text: "s"})

	require.Equal(t, []string{"A", "B"}, markedNames(p),
		"a bulk verb folds the open range into marks so there is one N-marks path")
	require.False(t, p.Visual.On())
}

func TestVisual_BindingIsSharedAndNotDangerous(t *testing.T) {
	t.Parallel()

	p := newPage(t)
	var found bool
	for _, b := range p.Bindings() {
		if b.Key != "Shift+V" {
			continue
		}
		found = true
		require.True(t, b.Shared, "the range verb folds into the GENERAL help column like Space")
		require.False(t, b.Dangerous, "anchoring a range mutates nothing remote")
	}
	require.True(t, found, "alerts page binds Shift+V")
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

	out := testutil.StripStyle(p.View(80, 24))
	for _, name := range []string{"A", "B"} {
		require.Contains(t, rowContaining(t, out, " "+name+" "), "✓",
			"the previewed rows carry the mark glyph before the commit")
	}
	for _, name := range []string{"C", "D"} {
		require.NotContains(t, rowContaining(t, out, " "+name+" "), "✓",
			"rows outside the range stay unmarked")
	}
	require.Empty(t, p.marks, "the preview is not yet in marks")
}

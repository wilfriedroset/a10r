// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// TestForm_BubblesStylesAreFlattened locks in the visual
// contract for every field row by inspecting the bubbles models'
// Styles directly, so the assertions don't rely on accidents of
// the active theme palette or the order lipgloss happens to
// emit SGR attributes today.
//
// Both the focused and the blurred state of every input must
// have:
//   - no foreground (terminal default)
//   - no background (no stripe behind the row)
//   - no bold/italic/underline (bubbles ships none, kept honest)
//
// — for the Text and Placeholder slots. The textarea's
// CursorLine slot must also be bare so its active line doesn't
// paint a `\x1b[40m`-style highlight behind the matchers buffer.
func TestForm_BubblesStylesAreFlattened(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})

	// textinput slots — every scalar field is built by newInput,
	// so checking one is enough as long as that's the only path.
	ti := f.starts.Styles()
	requireBareStyle(t, "textinput Focused.Text", ti.Focused.Text)
	requireBareStyle(t, "textinput Blurred.Text", ti.Blurred.Text)
	requireBareStyle(t, "textinput Focused.Placeholder", ti.Focused.Placeholder)
	requireBareStyle(t, "textinput Blurred.Placeholder", ti.Blurred.Placeholder)

	// textarea slots — the cursor-line highlight is the one most
	// likely to regress on a bubbles upgrade because its default
	// has a bg colour by design.
	ta := f.matchers.Styles()
	requireBareStyle(t, "textarea Focused.Text", ta.Focused.Text)
	requireBareStyle(t, "textarea Blurred.Text", ta.Blurred.Text)
	requireBareStyle(t, "textarea Focused.Placeholder", ta.Focused.Placeholder)
	requireBareStyle(t, "textarea Blurred.Placeholder", ta.Blurred.Placeholder)
	requireBareStyle(t, "textarea Focused.CursorLine", ta.Focused.CursorLine)
	requireBareStyle(t, "textarea Blurred.CursorLine", ta.Blurred.CursorLine)
}

// requireBareStyle asserts that s carries no foreground,
// background, or text-decoration attributes. Used to check that
// every bubbles slot has been flattened so the form rows render
// at the body's default fg with no bg stripe regardless of focus.
// Inspects lipgloss.Style's getters directly so the assertion
// stays valid across theme tweaks and lipgloss version bumps.
//
// Lipgloss returns lipgloss.NoColor{} (not nil) for an unset
// fg/bg slot — that's the sentinel "render with no SGR colour
// attribute" — so we compare against it rather than nil.
func requireBareStyle(t *testing.T, name string, s lipgloss.Style) {
	t.Helper()
	require.Equal(t, lipgloss.NoColor{}, s.GetForeground(), "%s: must not set a foreground", name)
	require.Equal(t, lipgloss.NoColor{}, s.GetBackground(), "%s: must not set a background", name)
	require.False(t, s.GetBold(), "%s: must not set bold", name)
	require.False(t, s.GetItalic(), "%s: must not set italic", name)
	require.False(t, s.GetUnderline(), "%s: must not set underline", name)
}

// TestForm_FieldRowLabelsAreBoldFgOnly asserts the row label
// renders with a foreground colour AND bold, but no background.
// The label is the only thing we paint with theme colours; an
// accidental Body.Default render here would drag the page bg
// behind every label cell.
func TestForm_FieldRowLabelsAreBoldFgOnly(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	row := f.fieldRow("Starts", fieldStarts, "value")
	// Bold SGR (`\x1b[1`) must appear; lipgloss may interleave
	// other codes, so the assertion is on substring presence.
	require.Contains(t, row, "\x1b[1", "blurred label must be rendered bold")
	// Backgrounds in lipgloss output land as `48;…m` segments.
	// None should appear in a bare label row.
	require.NotContains(t, row, "48;2;", "blurred label must not paint a 24-bit background")
	require.NotContains(t, row, "48;5;", "blurred label must not paint an 8-bit background")
}

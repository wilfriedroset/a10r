// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// TestFgOnly_AppliesRealForeground confirms a non-default colour is
// piped through to the resulting style's foreground.
func TestFgOnly_AppliesRealForeground(t *testing.T) {
	t.Parallel()
	c := lipgloss.Color("#aabbcc")

	style := FgOnly(c)

	require.Equal(t, c, style.GetForeground(),
		"FgOnly must set the foreground to the supplied colour")
	require.True(t, isUnsetColor(style.GetBackground()),
		"FgOnly must leave the background unset")
}

// TestFgOnly_LeavesTerminalDefaultUnset confirms the sentinel is not
// propagated as a real foreground — passing the sentinel must yield
// an unset foreground so the terminal's native fg shows through.
func TestFgOnly_LeavesTerminalDefaultUnset(t *testing.T) {
	t.Parallel()
	var c color.Color = terminalDefault{}

	style := FgOnly(c)

	require.True(t, isUnsetColor(style.GetForeground()),
		"FgOnly must skip the foreground call for the terminal-default sentinel")
}

// TestFgOnly_TerminalDefaultEmitsNoSGR pins the user-visible promise
// of the sentinel branch: rendering through a sentinel-derived style
// must not emit any SGR escape, so the terminal's native fg shows
// through unchanged on transparent skins.
func TestFgOnly_TerminalDefaultEmitsNoSGR(t *testing.T) {
	t.Parallel()
	var c color.Color = terminalDefault{}

	out := FgOnly(c).Render("hello")

	require.NotContains(t, out, "\x1b[",
		"FgOnly with the terminal-default sentinel must render without SGR escapes; got %q", out)
}

func TestSeverityStyle_ForLabel(t *testing.T) {
	t.Parallel()

	s := SeverityStyle{
		Critical: FgOnly(lipgloss.Color("#f00")),
		Warning:  FgOnly(lipgloss.Color("#fa0")),
		Info:     FgOnly(lipgloss.Color("#0af")),
		Unknown:  FgOnly(lipgloss.Color("#888")),
	}
	for _, tc := range []struct {
		label string
		want  lipgloss.Style
	}{
		{"critical", s.Critical},
		{"Critical", s.Critical},
		{"warning", s.Warning},
		{"info", s.Info},
		{"fatal", s.Unknown},
		{"—", s.Unknown},
		{"", s.Unknown},
	} {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want.GetForeground(), s.ForLabel(tc.label).GetForeground())
		})
	}
}

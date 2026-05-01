// SPDX-License-Identifier: Apache-2.0

package panel

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

// stripStyle drops ANSI SGR sequences so substring assertions
// don't pin colour values.
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

func TestRenderTop_AllColumnsAppear(t *testing.T) {
	t.Parallel()
	styles := loadStyles(t)
	out := RenderTop(State{
		Width: 120,
		Info: []InfoLine{
			{Label: "tenants", Value: "prod"},
			{Label: "version", Value: "v0.1.0"},
		},
		Hints: []action.Action{
			{Key: "s", Description: "silence"},
			{Key: "?", Description: "help"},
		},
		Logo: Logo,
	}, styles)
	visible := stripStyle(out)
	require.Contains(t, visible, "tenants:")
	require.Contains(t, visible, "prod")
	require.Contains(t, visible, "<s>")
	require.Contains(t, visible, "silence")
	require.Contains(t, visible, "<?>")
	require.Contains(t, visible, "a") // logo art has 'a'-shaped runes
}

func TestRenderTop_NarrowDropsLogo(t *testing.T) {
	t.Parallel()
	styles := loadStyles(t)
	// Width too tight for the logo: the renderer must drop it
	// rather than overflow.
	out := RenderTop(State{
		Width: 50,
		Info:  []InfoLine{{Label: "tenants", Value: "prod"}},
		Hints: []action.Action{{Key: "s", Description: "silence"}},
		Logo:  Logo,
	}, styles)
	require.NotContains(t, stripStyle(out), "a10r-logo-marker",
		"logo column must drop when width is tight (no specific glyph required)")
}

func TestRenderBody_TitleInTopBorder(t *testing.T) {
	t.Parallel()
	out := RenderBody(40, 6, "row1\nrow2", "alerts[2]", "", loadStyles(t))
	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 4, "frame must have top + bottom + body lines")
	require.Contains(t, lines[0], "alerts[2]",
		"title must appear in the top border")
	require.True(t, strings.HasPrefix(lines[0], "┌"))
	require.True(t, strings.HasSuffix(lines[0], "┐"))
	require.True(t, strings.HasPrefix(lines[len(lines)-1], "└"))
	require.True(t, strings.HasSuffix(lines[len(lines)-1], "┘"))
}

func TestRenderBody_FooterInBottomBorder(t *testing.T) {
	t.Parallel()
	// The footer label sits in the bottom border the same way the
	// title sits in the top — k9s symmetry. Pages use it for
	// ambient state ("next refresh 26s") that should be framed
	// rather than spend a body line.
	out := RenderBody(40, 6, "row1", "alerts[2]", "next refresh 26s", loadStyles(t))
	lines := strings.Split(out, "\n")
	bottom := lines[len(lines)-1]
	require.Contains(t, bottom, "next refresh 26s",
		"footer must appear in the bottom border")
	require.True(t, strings.HasPrefix(bottom, "└"))
	require.True(t, strings.HasSuffix(bottom, "┘"))
	require.NotContains(t, lines[0], "next refresh",
		"footer must not leak into the top border")
}

func TestRenderBody_EmptyFooterIsPlainRule(t *testing.T) {
	t.Parallel()
	out := RenderBody(40, 6, "row1", "alerts[2]", "", loadStyles(t))
	lines := strings.Split(out, "\n")
	bottom := lines[len(lines)-1]
	// A plain bottom rule is "└" + (innerWidth × "─") + "┘". With
	// innerWidth = 38, that's 38 box-drawing dashes between corners
	// and no label substring.
	require.Equal(t, "└"+strings.Repeat("─", 38)+"┘", bottom,
		"empty footer must render the bottom border as a plain rule")
}

func TestRenderBody_PadsAndTruncatesLines(t *testing.T) {
	t.Parallel()
	out := RenderBody(20, 4, "short\nthis-line-is-far-too-long-to-fit", "x", "", loadStyles(t))
	for l := range strings.SplitSeq(out, "\n") {
		require.LessOrEqual(t, len(l), 60,
			"each rendered line must fit (with byte allowance for box-drawing UTF-8)")
	}
}

func TestRenderFrame_WrapsBodyInBorderedBox(t *testing.T) {
	t.Parallel()
	out := RenderFrame(20, "🐩> typed", loadStyles(t))
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 3,
		"the prompt frame is exactly 3 lines: top border, body, bottom border")
	require.True(t, strings.HasPrefix(lines[0], "┌"))
	require.True(t, strings.HasSuffix(lines[0], "┐"))
	require.True(t, strings.HasPrefix(lines[1], "│"))
	require.True(t, strings.HasSuffix(lines[1], "│"))
	require.Contains(t, lines[1], "🐩> typed")
	require.True(t, strings.HasPrefix(lines[2], "└"))
	require.True(t, strings.HasSuffix(lines[2], "┘"))
}

func TestRenderFrame_TooNarrowFallsBackToBody(t *testing.T) {
	t.Parallel()
	// Narrower than the border can carry — return the body verbatim
	// rather than draw a degenerate frame the user would have to
	// stare at.
	require.Equal(t, "x", RenderFrame(2, "x", loadStyles(t)))
}

func TestTitle_ScopeAndCount(t *testing.T) {
	t.Parallel()
	require.Equal(t, "alerts[5]", Title("alerts", "", 5))
	require.Equal(t, "alerts(prod)[5]", Title("alerts", "prod", 5))
	require.Equal(t, "status(prod)", Title("status", "prod", 0))
	require.Equal(t, "alerts", Title("alerts", "", 0))
}

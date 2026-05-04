// SPDX-License-Identifier: Apache-2.0

package header

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

// loadDefaultStyles is the test bootstrap for theme.Styles. The
// header tests don't care which skin — they just need a populated
// Styles to render through.
func loadDefaultStyles(t *testing.T) theme.Styles {
	t.Helper()
	styles, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *styles
}

// stripStyle returns the visible text from a lipgloss-rendered
// string by walking and dropping ANSI escape sequences. Tests
// assert on the visible content so we don't pin the exact ANSI
// byte sequence (which can change across lipgloss versions).
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
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestRender_AllZonesAppear(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)
	hints := []action.Action{
		{Key: "s", Description: "silence", View: "alerts"},
		{Key: "?", Description: "help", View: ""},
	}

	out := Render(State{
		Tenants: "prod",
		Conn:    ConnConnected,
		Count:   "142 alerts",
		Age:     "5s ago",
		Content: "filter: severity=critical",
		Hints:   hints,
		Width:   120,
	}, styles)

	visible := stripStyle(out)
	require.Contains(t, visible, "tenants:")
	require.Contains(t, visible, "prod")
	require.Contains(t, visible, "●", "connected indicator")
	require.Contains(t, visible, "142 alerts")
	require.Contains(t, visible, "5s ago")
	require.Contains(t, visible, "filter: severity=critical")
	require.Contains(t, visible, "[s]")
	require.Contains(t, visible, "silence")
	require.Contains(t, visible, "[?]")
	require.Contains(t, visible, "help")
}

func TestRender_ConnStateGlyphs(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)
	cases := []struct {
		name  string
		state ConnState
		want  string
	}{
		{name: "connected", state: ConnConnected, want: "●"},
		{name: "degraded", state: ConnDegraded, want: "◐"},
		{name: "unreachable", state: ConnUnreachable, want: "○"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := stripStyle(Render(State{
				Tenants: "prod",
				Conn:    tc.state,
				Width:   80,
			}, styles))
			require.Contains(t, out, tc.want)
		})
	}
}

func TestRender_OmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)

	// No count, no age, no content, no hints → still renders the
	// minimum left zone (tenants: + glyph) and pads to width.
	out := Render(State{
		Tenants: "prod",
		Conn:    ConnConnected,
		Width:   60,
	}, styles)

	visible := stripStyle(out)
	require.Contains(t, visible, "tenants: prod")
	require.NotContains(t, visible, "·",
		"middle-dot separator must NOT appear when count and age are empty")
	require.NotContains(t, visible, "[",
		"hint strip brackets must NOT appear when no hints are set")
}

func TestRender_HintHelpKeyDistinguished(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)

	// `?` uses HelpKey colour; other keys use Key colour. The
	// produced strings differ in their ANSI prefix so we assert at
	// the styled level rather than after stripStyle.
	helpStyle := styles.Hint.HelpKey.Render("[?]")
	keyStyle := styles.Hint.Key.Render("[s]")
	require.NotEqual(t, helpStyle, keyStyle,
		"theme must give the ? help key a distinct colour")

	out := Render(State{
		Tenants: "prod",
		Width:   80,
		Hints: []action.Action{
			{Key: "s", View: "alerts", Description: "silence"},
			{Key: "?", View: "", Description: "help"},
		},
	}, styles)
	require.Contains(t, out, helpStyle, "rendered output must use HelpKey for ?")
	require.Contains(t, out, keyStyle, "rendered output must use Key for non-?")
}

func TestRender_ContentTruncation(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)
	long := strings.Repeat("filter:foo=bar ", 40) // way longer than 80 columns

	out := stripStyle(Render(State{
		Tenants: "prod",
		Conn:    ConnConnected,
		Content: long,
		Width:   80,
	}, styles))

	require.Contains(t, out, truncationMarker,
		"oversize content must be truncated with the marker")
	require.NotContains(t, out, strings.Repeat("filter:foo=bar ", 40),
		"oversize content must NOT appear in full")
}

func TestRender_NarrowWidthDropsMiddle(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)

	// 30 columns: left zone + right zone eat almost everything,
	// leaving the middle below the minMiddleWidth floor. Content
	// must be dropped entirely (no `…` orphan).
	out := stripStyle(Render(State{
		Tenants: "prod",
		Conn:    ConnConnected,
		Content: "filter: stuff",
		Hints: []action.Action{
			{Key: "s", Description: "silence", View: "alerts"},
		},
		Width: 30,
	}, styles))

	require.NotContains(t, out, "filter:",
		"narrow width must drop the middle zone entirely rather than render `…`")
}

func TestRender_NoBackgroundFill(t *testing.T) {
	t.Parallel()

	// The header strip sits inside the same unstyled chrome as the
	// `:` / `/` prompt. If we paint Header.Default's palette bg
	// behind the line it shows up as a coloured stripe on an
	// otherwise transparent canvas — the same regression that hit
	// the prompt before. Lock in: no SGR background parameter
	// (48;) appears in the rendered output, and the foreground
	// (38;) is still emitted.
	styles := loadDefaultStyles(t)
	out := Render(State{
		Tenants: "prod",
		Conn:    ConnConnected,
		Count:   "142 alerts",
		Age:     "5s ago",
		Content: "filter: severity=critical",
		Hints: []action.Action{
			{Key: "s", Description: "silence", View: "alerts"},
			{Key: "?", Description: "help", View: ""},
		},
		Width: 120,
	}, styles)

	require.Contains(t, out, "\x1b[38;",
		"header must still paint its foreground colour")
	require.NotContains(t, out, "\x1b[48;",
		"header must not paint a background colour — the surrounding chrome is unstyled")
	require.NotContains(t, out, ";48;",
		"header must not paint a background colour even when chained with fg in one SGR")
}

func TestRender_PadsToFullWidth(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)

	out := Render(State{
		Tenants: "prod",
		Conn:    ConnConnected,
		Width:   100,
	}, styles)
	require.Equal(t, 100, lipgloss.Width(out),
		"rendered header must occupy exactly state.Width columns")
}

func TestRender_WidthInvariantHoldsAtNarrowWidths(t *testing.T) {
	t.Parallel()

	// Regression test for the bug where a small Width caused the
	// right zone to overflow because the middleBudget went negative
	// and the gap clamped to zero. The right zone must shrink
	// (dropping trailing hints) to keep the total within Width.
	styles := loadDefaultStyles(t)
	hints := []action.Action{
		{Key: "s", Description: "silence", View: "alerts"},
		{Key: "Space", Description: "mark", View: "alerts"},
		{Key: "?", Description: "help", View: ""},
	}

	// At MinSensibleWidth and above, the invariant always holds even
	// when content + hints overflow. Below MinSensibleWidth the
	// header documents that left-zone overflow may occur (ANSI-
	// aware truncation of the styled left zone is out of scope for
	// v0.1).
	for _, width := range []int{MinSensibleWidth, 60, 80, 120, 200} {
		out := Render(State{
			Tenants: "prod",
			Conn:    ConnConnected,
			Count:   "142 alerts",
			Age:     "5s ago",
			Content: "filter: severity=critical AND alertname=~High.*",
			Hints:   hints,
			Width:   width,
		}, styles)
		require.Equal(t, width, lipgloss.Width(out),
			"width=%d (>= MinSensibleWidth): rendered header must occupy exactly width columns even when content + hints overflow",
			width)
	}
}

func TestRenderHintsWithBudget_DropsTrailingFirst(t *testing.T) {
	t.Parallel()

	styles := loadDefaultStyles(t)
	hints := []action.Action{
		{Key: "s", Description: "silence"},
		{Key: "Space", Description: "mark"},
		{Key: "?", Description: "help"},
	}

	// Generous budget keeps everything.
	full := renderHintsWithBudget(hints, 200, styles)
	require.Contains(t, stripStyle(full), "[s]")
	require.Contains(t, stripStyle(full), "[Space]")
	require.Contains(t, stripStyle(full), "[?]")

	// Tight budget drops trailing entries — `[s]` (registered first)
	// should survive longer than `[?]` (registered last).
	tight := renderHintsWithBudget(hints, 12, styles)
	require.Contains(t, stripStyle(tight), "[s]",
		"the first-registered hint must survive at the tightest budget")

	// Zero budget renders nothing.
	require.Empty(t, renderHintsWithBudget(hints, 0, styles))
}

func TestFormatAge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want string
	}{
		{name: "zero last → empty", last: time.Time{}, want: ""},
		{name: "sub-second → now", last: now.Add(-500 * time.Millisecond), want: "now"},
		{name: "5s ago", last: now.Add(-5 * time.Second), want: "5s ago"},
		{name: "59s ago", last: now.Add(-59 * time.Second), want: "59s ago"},
		{name: "2m ago", last: now.Add(-2 * time.Minute), want: "2m ago"},
		{name: "3h ago", last: now.Add(-3 * time.Hour), want: "3h ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, FormatAge(now, tc.last))
		})
	}
}

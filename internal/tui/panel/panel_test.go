// SPDX-License-Identifier: Apache-2.0

package panel

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// stripANSI removes SGR escape sequences from s. The frame border
// is now foreground-tinted so the raw string carries `\x1b[…m`
// prefixes / suffixes — assertions that pin border characters need
// the visible-glyph view, not the byte-level one.
var stripANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return stripANSI.ReplaceAllString(s, "") }

func TestRenderTop_AllColumnsAppear(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	out := RenderTop(State{
		Width:   120,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}, {Key: "1", Name: "prod"}},
		Hints: []action.Action{
			{Key: "s", Description: "silence"},
			{Key: "?", Description: "help"},
		},
		Logo: Logo,
	}, styles)
	visible := testutil.StripStyle(out)
	require.Contains(t, visible, "<0>")
	require.Contains(t, visible, "<1>")
	require.Contains(t, visible, "prod")
	require.Contains(t, visible, "<s>")
	require.Contains(t, visible, "silence")
	require.Contains(t, visible, "<?>")
	require.Contains(t, visible, "a") // logo art has 'a'-shaped runes
}

func TestRenderTop_NoInfoColumn(t *testing.T) {
	t.Parallel()
	// The body title already carries `alerts(scope)[N]` etc.; the
	// panel chrome stays free of `tenants:` / `alerts:` / `version:`
	// labels to keep the strip uncluttered.
	styles := testutil.LoadStyles(t)
	out := RenderTop(State{
		Width:   120,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}, {Key: "1", Name: "prod"}},
		Hints:   []action.Action{{Key: "s", Description: "silence"}, {Key: "?", Description: "help"}},
		Logo:    Logo,
	}, styles)
	visible := testutil.StripStyle(out)
	for _, label := range []string{"tenants:", "alerts:", "version:"} {
		require.NotContains(t, visible, label,
			"panel chrome must not duplicate body-title data with %q label", label)
	}
}

func TestRenderTop_NarrowDropsLogo(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// Width too tight for the logo: the renderer must drop it
	// rather than overflow.
	out := RenderTop(State{
		Width:   50,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}, {Key: "1", Name: "prod"}},
		Hints:   []action.Action{{Key: "s", Description: "silence"}},
		Logo:    Logo,
	}, styles)
	require.NotContains(t, testutil.StripStyle(out), "a10r-logo-marker",
		"logo column must drop when width is tight (no specific glyph required)")
}

func TestRenderTop_TenantsClipToLogoHeight(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	tenants := make([]TenantBinding, 16)
	for i := range tenants {
		tenants[i] = TenantBinding{Key: strconv.Itoa(i + 1), Name: fmt.Sprintf("t%02d", i+1)}
	}
	out := RenderTop(State{Width: 240, Tenants: tenants, Logo: Logo}, styles)
	visible := testutil.StripStyle(out)
	// 3 cols × 5 rows (logo height) = 15 cells. Item 16 must clip.
	require.Contains(t, visible, "t15")
	require.NotContains(t, visible, "t16",
		"the 16th tenant must clip — 3-col grid caps at logo height (5 rows)")
}

func TestRenderTop_TenantsColumnMajorFill(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// Six tenants, logo height 5 → cols=2, rows=5. Column-major:
	// <1>..<5> in col 0; <6> at top of col 1.
	tenants := []TenantBinding{
		{Key: "1", Name: "alpha"},
		{Key: "2", Name: "bravo"},
		{Key: "3", Name: "charlie"},
		{Key: "4", Name: "delta"},
		{Key: "5", Name: "echo"},
		{Key: "6", Name: "foxtrot"},
	}
	out := RenderTop(State{Width: 240, Tenants: tenants, Logo: Logo}, styles)
	lines := strings.Split(testutil.StripStyle(out), "\n")
	require.GreaterOrEqual(t, len(lines), 5)
	require.Contains(t, lines[0], "<1>")
	require.Contains(t, lines[0], "alpha")
	require.Contains(t, lines[0], "<6>",
		"<6> sits at the top of column 1 in column-major fill")
	require.Contains(t, lines[1], "<2>")
	require.NotContains(t, lines[1], "<6>",
		"<6> only appears in row 0; col 1 has just the one item")
}

func TestRenderTop_TenantsExactlyAtCapacity(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// 15 tenants exactly fill a 3-col × 5-row grid. The off-by-one
	// guard: every entry stays visible, none clipped.
	tenants := make([]TenantBinding, 15)
	for i := range tenants {
		tenants[i] = TenantBinding{Key: strconv.Itoa(i + 1), Name: fmt.Sprintf("t%02d", i+1)}
	}
	out := RenderTop(State{Width: 240, Tenants: tenants, Logo: Logo}, styles)
	visible := testutil.StripStyle(out)
	for i := 1; i <= 15; i++ {
		require.Contains(t, visible, fmt.Sprintf("t%02d", i),
			"every cell of an exactly-full grid must render")
	}
	lines := strings.Split(out, "\n")
	require.Len(t, lines, len(splitNonEmpty(Logo)),
		"exactly-full grid must not exceed the logo's row count")
}

func TestRenderBody_NarrowSubstitutesPlaceholder(t *testing.T) {
	t.Parallel()

	// Width below MinBodyWidth → body is replaced by the
	// "terminal too narrow" placeholder, but chrome (border +
	// title) still renders so the operator sees which view they
	// are on.
	body := "row1\nrow2"
	out := RenderBody(MinBodyWidth-10, 6, body, "alerts(all)[2]", "", testutil.LoadStyles(t))

	plainOut := plain(out)
	// Width 50, inner width 48 — full "terminal too narrow —
	// resize to >= 60 cols" message fits.
	require.Contains(t, plainOut, "resize to >= 60 cols",
		"narrow viewport must show the actionable cols-count hint")
	require.NotContains(t, plainOut, "row1",
		"actual body content must be hidden when narrow")
	require.Contains(t, plainOut, "alerts(all)[2]",
		"title chrome still renders so the operator sees the view")
}

func TestRenderBody_VeryNarrowKeepsColsHint(t *testing.T) {
	t.Parallel()

	// At inner width ~26 (frame width 28) the full message no
	// longer fits but the medium-tier "resize to >= 60 cols"
	// (20 cols) still does. The hint must NOT collapse to the
	// useless "narrow" sentinel while the cols count still fits.
	out := RenderBody(28, 6, "row1", "alerts", "", testutil.LoadStyles(t))
	plainOut := plain(out)
	require.Contains(t, plainOut, ">= 60 cols",
		"medium-tier variant keeps the actionable cols count")
	require.NotContains(t, plainOut, "narrow\n",
		"useless 'narrow' sentinel must not fire while cols hint fits")
}

func TestRenderBody_AtMinWidthRendersBody(t *testing.T) {
	t.Parallel()

	// At exactly MinBodyWidth the placeholder branch must NOT fire
	// — the threshold is "below", not "below or equal".
	out := RenderBody(MinBodyWidth, 4, "row1", "x", "", testutil.LoadStyles(t))
	require.Contains(t, plain(out), "row1")
	require.NotContains(t, plain(out), "narrow")
}

func TestRenderBody_TitleInTopBorder(t *testing.T) {
	t.Parallel()
	out := RenderBody(40, 6, "row1\nrow2", "alerts(all)[2]", "", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 4, "frame must have top + bottom + body lines")
	require.Contains(t, plain(lines[0]), "alerts(all)[2]",
		"title must appear in the top border")
	require.True(t, strings.HasPrefix(plain(lines[0]), "┌"))
	require.True(t, strings.HasSuffix(plain(lines[0]), "┐"))
	require.True(t, strings.HasPrefix(plain(lines[len(lines)-1]), "└"))
	require.True(t, strings.HasSuffix(plain(lines[len(lines)-1]), "┘"))
}

func TestRenderBody_TitleSegmentsUseDistinctStyles(t *testing.T) {
	t.Parallel()
	// k9s parity: subject + brackets share one colour, scope inside
	// `()` uses TitleHighlight, count inside `[]` uses TitleCounter
	// — three separate SGR sequences in the rendered output.
	styles := testutil.LoadStyles(t)
	out := RenderBody(60, 4, "row", "alerts(all)[300]", "", styles)
	lines := strings.Split(out, "\n")
	top := lines[0]

	// The styled title must contain three distinct SGR colour
	// sequences for the three roles. Each Frame.Title*  style only
	// sets a foreground, which renders as `\x1b[38;2;R;G;Bm`.
	titleFg := top
	require.Contains(t, titleFg, sgrFor(t, styles.Frame.Title.Bold(true).Render("alerts")),
		"subject must render in Frame.Title (bold)")
	require.Contains(t, titleFg, sgrFor(t, styles.Frame.TitleHighlight.Bold(true).Render("all")),
		"scope inside () must render in Frame.TitleHighlight (bold)")
	require.Contains(t, titleFg, sgrFor(t, styles.Frame.TitleCounter.Bold(true).Render("300")),
		"count inside [] must render in Frame.TitleCounter (bold)")
}

// sgrFor extracts the leading `\x1b[…m` opener from a rendered
// lipgloss snippet so the assertion can match by SGR signature
// rather than by exact rendered substring (the latter would match
// any text rendered with the same style; the former pins the
// style itself).
func sgrFor(t *testing.T, rendered string) string {
	t.Helper()
	loc := stripANSI.FindStringIndex(rendered)
	require.NotNil(t, loc, "rendered snippet should contain SGR opener")
	return rendered[loc[0]:loc[1]]
}

func TestRenderBody_FooterInBottomBorder(t *testing.T) {
	t.Parallel()
	// The footer label sits in the bottom border the same way the
	// title sits in the top — k9s symmetry. Pages use it for
	// ambient state ("next refresh 26s") that should be framed
	// rather than spend a body line.
	out := RenderBody(40, 6, "row1", "alerts[2]", "next refresh 26s", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	bottom := plain(lines[len(lines)-1])
	require.Contains(t, bottom, "next refresh 26s",
		"footer must appear in the bottom border")
	require.True(t, strings.HasPrefix(bottom, "└"))
	require.True(t, strings.HasSuffix(bottom, "┘"))
	require.NotContains(t, plain(lines[0]), "next refresh",
		"footer must not leak into the top border")
}

func TestRenderBody_EmptyFooterIsPlainRule(t *testing.T) {
	t.Parallel()
	out := RenderBody(40, 6, "row1", "alerts[2]", "", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	bottom := plain(lines[len(lines)-1])
	// A plain bottom rule is "└" + (innerWidth × "─") + "┘". With
	// innerWidth = 38, that's 38 box-drawing dashes between corners
	// and no label substring. SGR codes are stripped before the
	// equality check so the assertion stays semantic.
	require.Equal(t, "└"+strings.Repeat("─", 38)+"┘", bottom,
		"empty footer must render the bottom border as a plain rule")
}

func TestRenderBody_PadsAndTruncatesLines(t *testing.T) {
	t.Parallel()
	out := RenderBody(20, 4, "short\nthis-line-is-far-too-long-to-fit", "x", "", testutil.LoadStyles(t))
	for l := range strings.SplitSeq(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 20,
			"each rendered line's visual width must fit the requested width")
	}
}

func TestRenderFrame_WrapsBodyInBorderedBox(t *testing.T) {
	t.Parallel()
	out := RenderFrame(20, "🐩> typed", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 3,
		"the prompt frame is exactly 3 lines: top border, body, bottom border")
	require.True(t, strings.HasPrefix(plain(lines[0]), "┌"))
	require.True(t, strings.HasSuffix(plain(lines[0]), "┐"))
	require.True(t, strings.HasPrefix(plain(lines[1]), "│"))
	require.True(t, strings.HasSuffix(plain(lines[1]), "│"))
	require.Contains(t, lines[1], "🐩> typed")
	require.True(t, strings.HasPrefix(plain(lines[2]), "└"))
	require.True(t, strings.HasSuffix(plain(lines[2]), "┘"))
}

func TestRenderFrame_TooNarrowFallsBackToBody(t *testing.T) {
	t.Parallel()
	// Narrower than the border can carry — return the body verbatim
	// rather than draw a degenerate frame the user would have to
	// stare at.
	require.Equal(t, "x", RenderFrame(2, "x", testutil.LoadStyles(t)))
}

func TestTitle_ScopeAndCount(t *testing.T) {
	t.Parallel()
	require.Equal(t, "alerts[5]", Title("alerts", "", 5))
	require.Equal(t, "alerts(prod)[5]", Title("alerts", "prod", 5))
	require.Equal(t, "status(prod)", Title("status", "prod", 0))
	require.Equal(t, "alerts", Title("alerts", "", 0))
}

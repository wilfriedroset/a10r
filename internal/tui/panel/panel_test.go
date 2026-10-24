// SPDX-License-Identifier: Apache-2.0

package panel

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/action"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var sgrOpener = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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

func TestRenderTop_HintGridReflowsCols(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// Five hints with deliberately wide descriptions so the widest
	// hint cell is well over 20 cells. With logo dropped and tenants
	// taking their natural ~14-cell column, choose a width that lets
	// only 1 hint col fit. Every hint label must still appear (5
	// hints in 1 col × 5-row rowsBudget == fits exactly).
	hints := []action.Action{
		{Key: "s", Description: "silence-this-alert-now"},
		{Key: "r", Description: "refresh-data-immediately"},
		{Key: "w", Description: "watch-mode-toggle-live"},
		{Key: "f", Description: "filter-rows-by-pattern"},
		{Key: "?", Description: "show-help-overlay-now"},
	}
	state := State{
		Width:   46,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}, {Key: "1", Name: "prod"}},
		Hints:   hints,
		Logo:    Logo,
	}
	out := RenderTop(state, styles)
	visible := testutil.StripStyle(out)
	for _, h := range hints {
		require.Contains(t, visible, h.Description,
			"hint %q must remain visible after the reflow drops cols rather than chips",
			h.Description)
	}
	for l := range strings.SplitSeq(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), state.Width,
			"every rendered row must fit within state.Width after width-aware reflow")
	}
}

func TestRenderTop_HintGridDropsTrailingWhenWidestOverflows(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// Three short hints + one freakishly wide trailing hint. The
	// first three's cells fit at 1 col within availWidth, but the
	// fourth's cell exceeds availWidth on its own. The reflow must
	// drop the trailing chip from the end and recompute cellW.
	hints := []action.Action{
		{Key: "s", Description: "silence"},
		{Key: "r", Description: "refresh"},
		{Key: "w", Description: "watch"},
		{Key: "?", Description: "ridiculously-long-description-that-cannot-fit"},
	}
	state := State{
		Width:   40,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}},
		Hints:   hints,
		Logo:    Logo,
	}
	out := RenderTop(state, styles)
	visible := testutil.StripStyle(out)
	require.Contains(t, visible, "silence",
		"the first hint survives the trailing-drop reflow")
	require.Contains(t, visible, "refresh",
		"the second hint survives the trailing-drop reflow")
	require.Contains(t, visible, "watch",
		"the third hint survives the trailing-drop reflow")
	require.NotContains(t, visible, "ridiculously-long-description-that-cannot-fit",
		"the trailing oversize hint must drop entirely rather than overflow")
	for l := range strings.SplitSeq(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), state.Width,
			"every rendered row must fit within state.Width after the trailing drop")
	}
}

func TestRenderTop_HintGridPrependsHelpAffordance(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)

	// The hint grid's contract widens to "curated global prelude +
	// page bindings" — today the prelude is just `?` so a reader on
	// any page can spot the discovery gateway without opening it.
	// ADR 0038.
	out := RenderTop(State{
		Width:   120,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}},
		Hints:   []action.Action{{Key: "s", Description: "silence"}},
		Logo:    Logo,
	}, styles)
	visible := testutil.StripStyle(out)
	require.Contains(t, visible, "<?>",
		"hint grid must always surface `<?>` regardless of page bindings")
	require.Contains(t, visible, "help",
		"the `?` chip carries the `help` description so it reads at a glance")
	require.Less(t, strings.Index(visible, "<?>"), strings.Index(visible, "<s>"),
		"the curated prelude renders before the page bindings (top-left of column-major grid)")
}

func TestRenderTop_HintGridSurfacesHelpEvenWithEmptyPageBindings(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)

	// A page that returns nil Bindings (boot placeholder, empty
	// modal frame) still gets the discovery affordance — the
	// prelude is owned by the panel, not by any individual page.
	out := RenderTop(State{
		Width:   120,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}},
		Hints:   nil,
		Logo:    Logo,
	}, styles)
	require.Contains(t, testutil.StripStyle(out), "<?>",
		"empty page bindings must still surface the help chip")
}

func TestRenderTop_HintGridDoesNotDuplicateHelpWhenPageBindsIt(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)

	// Defensive: pages must not bind `?` (it's a LayerGlobal verb
	// per ADR 0019), but if a future page accidentally returns it,
	// the panel must not paint `<?>` twice.
	out := RenderTop(State{
		Width:   120,
		Tenants: []TenantBinding{{Key: "0", Name: "all"}},
		Hints: []action.Action{
			{Key: "?", Description: "help"},
			{Key: "s", Description: "silence"},
		},
		Logo: Logo,
	}, styles)
	visible := testutil.StripStyle(out)
	require.Equal(t, 1, strings.Count(visible, "<?>"),
		"the curated prelude must dedupe against a page that re-emits `?`")
}

func TestRenderTop_NeverExceedsStateWidth(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	widths := []int{40, 60, 80, 120, 200}
	hintCounts := []int{0, 1, 5, 12}
	tenantCounts := []int{1, 4, 10}
	for _, w := range widths {
		for _, hc := range hintCounts {
			for _, tc := range tenantCounts {
				hints := make([]action.Action, hc)
				for i := range hints {
					hints[i] = action.Action{
						Key:         fmt.Sprintf("h%d", i),
						Description: fmt.Sprintf("hint-action-%02d", i),
					}
				}
				tenants := make([]TenantBinding, tc)
				for i := range tenants {
					tenants[i] = TenantBinding{
						Key:  strconv.Itoa(i),
						Name: fmt.Sprintf("tenant-%02d", i),
					}
				}
				state := State{Width: w, Tenants: tenants, Hints: hints, Logo: Logo}
				out := RenderTop(state, styles)
				for l := range strings.SplitSeq(out, "\n") {
					require.LessOrEqual(t, lipgloss.Width(l), w,
						"hard-floor invariant: row exceeds state.Width "+
							"(width=%d hints=%d tenants=%d)", w, hc, tc)
				}
			}
		}
	}
}

func TestRenderTop_TenantsKeepNaturalWidth(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	// Four tenants → natural single-col grid of 4 rows. The tenant
	// cell width must not shrink when hints are squeezed; tenants
	// take their natural width and hints absorb whatever is left.
	tenants := []TenantBinding{
		{Key: "0", Name: "all"},
		{Key: "1", Name: "production-cluster"},
		{Key: "2", Name: "staging-cluster"},
		{Key: "3", Name: "dev-cluster"},
	}
	hints := []action.Action{
		{Key: "s", Description: "silence-this-alert-please"},
		{Key: "r", Description: "refresh-data-immediately"},
	}
	wide := RenderTop(State{Width: 240, Tenants: tenants, Hints: hints, Logo: Logo}, styles)
	narrow := RenderTop(State{Width: 60, Tenants: tenants, Hints: hints, Logo: Logo}, styles)

	require.Equal(t, tenantCellWidth(t, wide), tenantCellWidth(t, narrow),
		"tenant cells must keep their natural width on narrow terminals "+
			"(hints absorb the squeeze, tenants do not)")
}

// tenantCellWidth returns the natural width of the tenant column
// in the rendered top panel — the widest tenant cell, derived as
// the maximum across rows of `lipgloss.Width(line up to the first
// inter-zone gap)`. The widest tenant's row has zero internal
// right-pad so its split lands at the real inter-zone gap; shorter
// rows split earlier (inside their own right-pad) but contribute a
// smaller value to the max, so the result is the column's natural
// cell width regardless of which tenant is the widest.
func tenantCellWidth(t *testing.T, rendered string) int {
	t.Helper()
	w := 0
	for l := range strings.Lines(testutil.StripStyle(rendered)) {
		if !strings.HasPrefix(l, "<") {
			continue
		}
		before, _, _ := strings.Cut(l, "  ")
		if cw := lipgloss.Width(before); cw > w {
			w = cw
		}
	}
	return w
}

func TestRenderBody_NarrowSubstitutesPlaceholder(t *testing.T) {
	t.Parallel()

	// Width below MinBodyWidth → body is replaced by the
	// "terminal too narrow" placeholder, but chrome (border +
	// title) still renders so the operator sees which view they
	// are on.
	body := "row1\nrow2"
	out := RenderBody(MinBodyWidth-10, 6, body, "alerts(all)[2]", "", testutil.LoadStyles(t))

	plainOut := testutil.StripStyle(out)
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
	plainOut := testutil.StripStyle(out)
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
	require.Contains(t, testutil.StripStyle(out), "row1")
	require.NotContains(t, testutil.StripStyle(out), "narrow")
}

func TestRenderBody_TitleInTopBorder(t *testing.T) {
	t.Parallel()
	out := RenderBody(40, 6, "row1\nrow2", "alerts(all)[2]", "", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 4, "frame must have top + bottom + body lines")
	require.Contains(t, testutil.StripStyle(lines[0]), "alerts(all)[2]",
		"title must appear in the top border")
	require.True(t, strings.HasPrefix(testutil.StripStyle(lines[0]), "┌"))
	require.True(t, strings.HasSuffix(testutil.StripStyle(lines[0]), "┐"))
	require.True(t, strings.HasPrefix(testutil.StripStyle(lines[len(lines)-1]), "└"))
	require.True(t, strings.HasSuffix(testutil.StripStyle(lines[len(lines)-1]), "┘"))
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
	loc := sgrOpener.FindStringIndex(rendered)
	require.NotNil(t, loc, "rendered snippet should contain SGR opener")
	return rendered[loc[0]:loc[1]]
}

// TestRenderBody_PreStyledTitleTruncatesByVisibleWidth pins the
// SGR-aware clamp on the title: the app warn-tints the filter-error
// tag, so an over-long title arriving with escape bytes must be
// measured by its visible glyphs and must never be cut mid-sequence.
func TestRenderBody_PreStyledTitleTruncatesByVisibleWidth(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	title := "alerts(all)[2] </^web(> " + styles.Flash.Warn.Render("[regex: missing closing )]")

	top, _, _ := strings.Cut(RenderBody(48, 4, "row", title, "", styles), "\n")
	visible := testutil.StripStyle(top)

	require.Equal(t, 48, lipgloss.Width(top), "the clamped title still fills exactly one row")
	require.NotContains(t, visible, "\x1b",
		"a cut inside a CSI sequence would leave escape bytes in the border")
	require.Contains(t, visible, "alerts(all)[2] </^web(> [regex",
		"escape bytes must not spend the visible width budget")
	require.True(t, strings.HasPrefix(visible, "┌"))
	require.True(t, strings.HasSuffix(visible, "┐"))
}

func TestRenderBody_FooterInBottomBorder(t *testing.T) {
	t.Parallel()
	// The footer label sits in the bottom border the same way the
	// title sits in the top — k9s symmetry. Pages use it for
	// ambient state ("next refresh 26s") that should be framed
	// rather than spend a body line.
	out := RenderBody(40, 6, "row1", "alerts[2]", "next refresh 26s", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	bottom := testutil.StripStyle(lines[len(lines)-1])
	require.Contains(t, bottom, "next refresh 26s",
		"footer must appear in the bottom border")
	require.True(t, strings.HasPrefix(bottom, "└"))
	require.True(t, strings.HasSuffix(bottom, "┘"))
	require.NotContains(t, testutil.StripStyle(lines[0]), "next refresh",
		"footer must not leak into the top border")
}

func TestRenderBody_EmptyFooterIsPlainRule(t *testing.T) {
	t.Parallel()
	out := RenderBody(40, 6, "row1", "alerts[2]", "", testutil.LoadStyles(t))
	lines := strings.Split(out, "\n")
	bottom := testutil.StripStyle(lines[len(lines)-1])
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
	require.True(t, strings.HasPrefix(testutil.StripStyle(lines[0]), "┌"))
	require.True(t, strings.HasSuffix(testutil.StripStyle(lines[0]), "┐"))
	require.True(t, strings.HasPrefix(testutil.StripStyle(lines[1]), "│"))
	require.True(t, strings.HasSuffix(testutil.StripStyle(lines[1]), "│"))
	require.Contains(t, lines[1], "🐩> typed")
	require.True(t, strings.HasPrefix(testutil.StripStyle(lines[2]), "└"))
	require.True(t, strings.HasSuffix(testutil.StripStyle(lines[2]), "┘"))
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

func TestRenderTop_Layout(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	const logo = "LOGO\nLOGO\nLOGO"
	hints := []action.Action{
		{Key: "s", Description: "silence"},
		{Key: "r", Description: "refresh"},
		{Key: "w", Description: "watch"},
		{Key: "x", Description: "expire"},
	}
	tests := []struct {
		name      string
		state     State
		wantChips [][]string
		wantLogo  bool
	}{
		{
			name: "logo sits flush right when everything fits",
			state: State{
				Width:   40,
				Tenants: []TenantBinding{{Key: "0", Name: "all"}, {Key: "1", Name: "prod"}},
				Hints:   []action.Action{{Key: "s", Description: "silence"}},
				Logo:    logo,
			},
			wantChips: [][]string{{"<0>", "<?>"}, {"<1>", "<s>"}, nil},
			wantLogo:  true,
		},
		{
			name: "tenants past the logo height spread into columns",
			state: State{
				Width: 60,
				Tenants: []TenantBinding{
					{Key: "1", Name: "a"},
					{Key: "2", Name: "b"},
					{Key: "3", Name: "c"},
					{Key: "4", Name: "d"},
					{Key: "5", Name: "e"},
					{Key: "6", Name: "f"},
					{Key: "7", Name: "g"},
				},
				Logo: logo,
			},
			wantChips: [][]string{{"<1>", "<4>", "<7>", "<?>"}, {"<2>", "<5>"}, {"<3>", "<6>"}},
			wantLogo:  true,
		},
		{
			name:      "dropped logo lets hints reflow into two columns",
			state:     State{Width: 36, Tenants: []TenantBinding{{Key: "0", Name: "all"}}, Hints: hints, Logo: logo},
			wantChips: [][]string{{"<0>", "<?>", "<w>"}, {"<s>", "<x>"}, {"<r>"}},
		},
		{
			name:      "one hint column clips hints past the logo height",
			state:     State{Width: 30, Tenants: []TenantBinding{{Key: "0", Name: "all"}}, Hints: hints, Logo: logo},
			wantChips: [][]string{{"<0>", "<?>"}, {"<s>"}, {"<r>"}},
		},
		{
			name:      "two hint columns at exactly their width",
			state:     State{Width: 24, Hints: hints, Logo: logo},
			wantChips: [][]string{{"<?>", "<w>"}, {"<s>", "<x>"}, {"<r>"}},
		},
		{
			name:      "one column short of two hint columns",
			state:     State{Width: 23, Hints: hints, Logo: logo},
			wantChips: [][]string{{"<?>"}, {"<s>"}, {"<r>"}},
		},
		{
			name:      "logo stays when the zones fill the width exactly",
			state:     State{Width: 17, Hints: []action.Action{{Key: "s", Description: "silence"}}, Logo: logo},
			wantChips: [][]string{{"<?>"}, {"<s>"}, nil},
			wantLogo:  true,
		},
		{
			name:      "logo drops one column short of the zones",
			state:     State{Width: 16, Hints: []action.Action{{Key: "s", Description: "silence"}}, Logo: logo},
			wantChips: [][]string{{"<?>"}, {"<s>"}},
		},
		{
			name: "hint as wide as the space left stays",
			state: State{
				Width:   30,
				Tenants: []TenantBinding{{Key: "0", Name: "all"}},
				Hints:   []action.Action{{Key: "s", Description: "silence"}, {Key: "x", Description: "expire-everything"}},
				Logo:    logo,
			},
			wantChips: [][]string{{"<0>", "<?>"}, {"<s>"}, {"<x>"}},
		},
		{
			name: "hint wider than the space left drops",
			state: State{
				Width:   30,
				Tenants: []TenantBinding{{Key: "0", Name: "all"}},
				Hints:   []action.Action{{Key: "s", Description: "silence"}, {Key: "x", Description: "expire-everything-x"}},
				Logo:    logo,
			},
			wantChips: [][]string{{"<0>", "<?>"}, {"<s>"}},
		},
		{
			name:      "without a logo every hint stacks in one column",
			state:     State{Width: 30, Hints: hints},
			wantChips: [][]string{{"<?>"}, {"<s>"}, {"<r>"}, {"<w>"}, {"<x>"}},
		},
	}
	chipRE := regexp.MustCompile(`<[^>]+>`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := strings.Split(testutil.StripStyle(RenderTop(tt.state, styles)), "\n")
			require.Len(t, lines, len(tt.wantChips))

			labels := map[string]string{"<?>": "help"}
			isTenant := map[string]bool{}
			tenantCellW := 0
			for _, b := range tt.state.Tenants {
				chip := "<" + b.Key + ">"
				labels[chip], isTenant[chip] = b.Name, true
				tenantCellW = max(tenantCellW, len(chip)+1+len(b.Name))
			}
			for _, h := range tt.state.Hints {
				labels["<"+h.Key+">"] = h.Description
			}
			hintCellW := 0
			for chip, label := range labels {
				if !isTenant[chip] && slices.ContainsFunc(tt.wantChips, func(row []string) bool { return slices.Contains(row, chip) }) {
					hintCellW = max(hintCellW, len(chip)+1+len(label))
				}
			}
			hintX := 0
			if n := len(tt.state.Tenants); n > 0 {
				cols := len(slices.DeleteFunc(slices.Clone(tt.wantChips[0]), func(c string) bool { return !isTenant[c] }))
				hintX = cols*tenantCellW + (cols-1)*colGap + colGap
			}

			for i, line := range lines {
				require.Equal(t, tt.state.Width, lipgloss.Width(line), "row %d=%q", i, line)
				if tt.wantLogo {
					require.Equal(t, tt.state.Width-len("LOGO"), strings.Index(line, "LOGO"), "row %d=%q", i, line)
				} else {
					require.NotContains(t, line, "LOGO")
				}
				locs := chipRE.FindAllStringIndex(line, -1)
				var chips []string
				firstHint := true
				for j, loc := range locs {
					chip := line[loc[0]:loc[1]]
					chips = append(chips, chip)
					require.True(t, strings.HasPrefix(line[loc[1]+1:], labels[chip]),
						"label of %s sits one column after the chip in row %q", chip, line)
					if !isTenant[chip] && firstHint {
						require.Equal(t, hintX, loc[0], "hint column start in row %q", line)
						firstHint = false
					}
					if j == 0 || isTenant[chip] != isTenant[line[locs[j-1][0]:locs[j-1][1]]] {
						continue
					}
					cellW := hintCellW
					if isTenant[chip] {
						cellW = tenantCellW
					}
					require.Equal(t, cellW+colGap, loc[0]-locs[j-1][0], "grid column offset in row %q", line)
				}
				require.Equal(t, tt.wantChips[i], chips, "row %d=%q", i, line)
			}
		})
	}
}

func TestRenderTop_TenantsWiderThanTheWidthClipAtTheEdge(t *testing.T) {
	t.Parallel()
	out := RenderTop(State{
		Width:   10,
		Tenants: []TenantBinding{{Key: "0", Name: "production"}},
		Logo:    "LOGO\nLOGO\nLOGO",
	}, testutil.LoadStyles(t))
	require.Equal(t, "<0> produc", testutil.StripStyle(out))
}

func TestRenderTop_ChipAndLogoStyles(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	out := RenderTop(State{
		Width: 40,
		Hints: []action.Action{{Key: "s", Description: "silence"}},
		Logo:  "LOGO",
	}, styles)
	require.Contains(t, out, styles.Hint.HelpKeyBold.Render("<?>"))
	require.Contains(t, out, styles.Hint.KeyBold.Render("<s>"))
	require.Contains(t, out, styles.Body.Logo.Render("LOGO"))
}

func TestRenderBody_Layout(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	row := func(s string) string { return "│" + s + strings.Repeat(" ", 58-len(s)) + "│" }
	rule := func(l, r string) string { return l + strings.Repeat("─", 58) + r }
	exact := strings.Repeat("e", 58)
	tests := []struct {
		name          string
		width, height int
		body          string
		want          []string
	}{
		{
			name:  "lines pad, clip and stop at the inner height",
			width: 60, height: 6,
			body: "short\n" + exact + "\n" + exact + "-overflow\nlast\nhidden",
			want: []string{rule("┌", "┐"), row("short"), row(exact), row(exact), row("last"), rule("└", "┘")},
		},
		{
			name:  "short body pads to the inner height",
			width: 60, height: 4,
			body: "only",
			want: []string{rule("┌", "┐"), row("only"), row(""), rule("└", "┘")},
		},
		{
			name:  "too narrow for chrome returns the body",
			width: 3, height: 5,
			body: "row1",
			want: []string{"row1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RenderBody(tt.width, tt.height, tt.body, "", "", styles)
			require.Equal(t, tt.want, strings.Split(testutil.StripStyle(got), "\n"))
		})
	}
}

func TestRenderBody_NarrowFrame(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	tests := []struct {
		name          string
		width, height int
		wantMsg       string
	}{
		{name: "narrow placeholder centres vertically", width: 30, height: 7, wantMsg: "resize to >= 60 cols"},
		{name: "placeholder variant as wide as the inner width fits", width: 22, height: 3, wantMsg: "resize to >= 60 cols"},
		{name: "placeholder one column too wide falls back to the shorter variant", width: 21, height: 3, wantMsg: ">= 60 cols"},
		{name: "smallest frame still draws chrome", width: 4, height: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := strings.Split(testutil.StripStyle(RenderBody(tt.width, tt.height, "row1", "", "", styles)), "\n")
			require.Len(t, lines, tt.height)
			for _, l := range lines {
				require.Equal(t, tt.width, lipgloss.Width(l), "row=%q", l)
			}
			last := lines[len(lines)-1]
			require.True(t, strings.HasPrefix(lines[0], "┌") && strings.HasSuffix(lines[0], "┐"), "top=%q", lines[0])
			require.True(t, strings.HasPrefix(last, "└") && strings.HasSuffix(last, "┘"), "bottom=%q", last)
			if tt.wantMsg == "" {
				return
			}
			msgRow := lines[(tt.height-2)/2+1]
			require.True(t, strings.HasPrefix(msgRow, "│"), "row=%q", msgRow)
			require.Equal(t, tt.wantMsg, strings.TrimSpace(strings.Trim(msgRow, "│")))
		})
	}
}

func TestRenderFrame_Layout(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	tests := []struct {
		name  string
		width int
		body  string
		want  []string
	}{
		{name: "smallest frame", width: 4, body: "x", want: []string{"┌──┐", "│x │", "└──┘"}},
		{name: "short body pads", width: 10, body: "abc", want: []string{"┌────────┐", "│abc     │", "└────────┘"}},
		{name: "long body clips", width: 6, body: "abcdefgh", want: []string{"┌────┐", "│abcd│", "└────┘"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, strings.Split(testutil.StripStyle(RenderFrame(tt.width, tt.body, styles)), "\n"))
		})
	}
}

// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// TestForm_BulkModeRendersBanner asserts the bulk View renders
// the BulkBanner literal alongside the "Targets" label and omits
// the matchers placeholder.
func TestForm_BulkModeRendersBanner(t *testing.T) {
	t.Parallel()

	// Pick a banner short enough to fit one line inside the
	// 120-col View — long banners wrap to the input width, which
	// is correct behaviour but would break a literal substring
	// match. Real-world banners (e.g. "applies to 5 alerts
	// across 2 tenants — each silenced with its own labels") may
	// well wrap; the wrap shape is incidental, the verbatim
	// presence in the rendered view is what we care about.
	banner := "applies to 5 alerts; per-target labels"
	f := newBulkForm(t, nil, banner)

	view := f.View(120, 24)
	require.Contains(t, view, banner, "bulk View must render the banner string verbatim")
	require.Contains(t, view, "Targets", "bulk View labels the slot 'Targets' so the user knows the matchers are per-target")
	require.NotContains(t, view, "alertname=HighCPU",
		"bulk View must NOT render the matchers placeholder — the buffer is hidden")
}

// TestForm_TenantRowHintAdvertisesEnter pins the discoverability
// affordance: when the Tenant row is editable the rendered view
// must include "[Enter to change]" so the user does not have to
// guess that Enter opens the picker. Disabled variants (single-
// tenant, edit-mode) must NOT show the hint because the affordance
// is inert there.
func TestForm_TenantRowHintAdvertisesEnter(t *testing.T) {
	t.Parallel()

	// Anchor on the stable token, not the literal punctuation —
	// a future theming pass that wraps the brackets in a styled
	// span shouldn't flake the contract that the affordance is
	// surfaced.
	const hintToken = "Enter to change"

	multi := newMultiTenantForm(t, &fakeClient{}, &fakeClient{})
	require.Contains(t, multi.View(120, 24), hintToken,
		"editable Tenant row must advertise the Enter-to-change affordance")

	single := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.NotContains(t, single.View(120, 24), hintToken,
		"disabled single-tenant Tenant row must not advertise an inert affordance")

	edit := New(Options{
		Clients: map[string]Client{"prod": &fakeClient{}, "staging": &fakeClient{}},
		Tenant:  "prod",
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
		EditID:  "sil-7",
	})
	require.NotContains(t, edit.View(120, 24), hintToken,
		"edit-mode Tenant row is read-only and must not advertise the picker")

	// Narrow form: hint must elide rather than force a wrap that
	// breaks fieldRow's continuation-padding grid. Width 30 leaves
	// ~17 cols for the value column once label/prefix are subtracted,
	// well below "prod" (4) + "  [Enter to change]" (21).
	require.NotContains(t, multi.View(30, 24), hintToken,
		"narrow-width Tenant row must elide the hint to keep the grid aligned")
}

// TestForm_BulkModeNoTenantRow asserts that bulk mode omits the
// Tenant row entirely (the Targets banner is the source of truth
// for the per-tenant breakdown in bulk). EditID + Bulk is mutually
// exclusive per the existing comment, so this is the only path
// that skips the row outright rather than rendering it disabled.
func TestForm_BulkModeNoTenantRow(t *testing.T) {
	t.Parallel()

	f := newBulkForm(t, &fakeClient{}, "applies to 3 alerts across 2 tenants")
	view := f.View(120, 24)
	require.NotContains(t, view, "Tenant:",
		"bulk View must NOT render the Tenant row — the Targets banner is the source of truth")
	require.Contains(t, view, "Targets",
		"bulk View must keep the existing Targets banner label")
}

// TestForm_BubblesStylesAreFlattened locks in the visual
// contract for the typed-text and cursor-line slots by
// inspecting the bubbles models' Styles directly. Asserting on
// the style structs (not rendered output) keeps the test stable
// against theme tweaks and lipgloss SGR-ordering changes.
//
// Both the focused and the blurred state of every input must
// have a bare Text style — no fg, bg, or text-decoration —
// because the form's focus marker is the leading `▸` plus the
// accent-tinted label, and bubbles' default dim-grey blurred
// text would make filled rows read as stale.
//
// The textarea's CursorLine slot in both states must also be
// bare so its active line doesn't paint a `\x1b[40m`-style
// highlight behind the matchers buffer.
//
// The Placeholder slots are NOT asserted bare here — per
// ADR-0012 we keep the bubbles default dim foreground on both
// focused and blurred placeholders so empty fields are
// distinguishable from filled ones. TestForm_PlaceholderRendersDim
// covers the placeholder-dim rendering contract.
func TestForm_BubblesStylesAreFlattened(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})

	// textinput slots — every scalar field is built by newInput,
	// so checking one is enough as long as that's the only path.
	ti := f.starts.Styles()
	requireBareStyle(t, "textinput Focused.Text", ti.Focused.Text)
	requireBareStyle(t, "textinput Blurred.Text", ti.Blurred.Text)

	// textarea slots — the cursor-line highlight is the one most
	// likely to regress on a bubbles upgrade because its default
	// has a bg colour by design.
	ta := f.matchers.Styles()
	requireBareStyle(t, "textarea Focused.Text", ta.Focused.Text)
	requireBareStyle(t, "textarea Blurred.Text", ta.Blurred.Text)
	requireBareStyle(t, "textarea Focused.CursorLine", ta.Focused.CursorLine)
	requireBareStyle(t, "textarea Blurred.CursorLine", ta.Blurred.CursorLine)
}

// placeholderDimSGR is the SGR sequence bubbles' default
// placeholder style emits — foreground colour 240 in the 256-
// colour palette, no background. Anchored on
// textinput.DefaultStyles(true).Focused.Placeholder so a
// bubbles upgrade that picks a different shade is caught here
// rather than by the rendering tests below.
func placeholderDimSGR(t *testing.T) string {
	t.Helper()
	rendered := textinput.DefaultStyles(true).Focused.Placeholder.Render("x")
	// Strip the trailing reset and the placeholder rune so we
	// keep just the leading SGR — that's the prefix every
	// placeholder render must carry.
	idx := strings.Index(rendered, "x")
	require.Positive(t, idx, "default placeholder must wrap text in an SGR prefix")
	return rendered[:idx]
}

// TestForm_FocusedEmptyScalarRendersPlaceholderDim guards the
// ADR-0012 contract on the focused-empty path: the Creator
// field's placeholder ("$USER") must render with the bubbles
// default dim foreground, not at the body's default fg,
// otherwise the empty-vs-filled cue collapses.
//
// Bubbles renders the first placeholder rune as the virtual
// cursor on the focused row (so it gets a reverse-video SGR,
// not the placeholder one); the remaining "USER" carries the
// dim placeholder style, which is what we anchor on.
func TestForm_FocusedEmptyScalarRendersPlaceholderDim(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		// Deliberately omit Creator so the field is empty and
		// renders the placeholder.
	})
	// Walk focus to Creator: default focus is Matchers, then
	// Starts, Ends, Creator.
	for f.focus != fieldCreator {
		_ = f.cycleFocus(1)
	}

	view := f.View(120, 24)
	dim := placeholderDimSGR(t)
	// Anchor on a short prefix of the placeholder remainder rather
	// than the full contiguous "USER": bubbles is free to interleave
	// another SGR inside the placeholder render (cursor styling,
	// future hint markers), and a shorter anchor survives that
	// without giving up the contract that the dim SGR sits next to
	// the placeholder text. "U" is still unique here vs. bubbles'
	// reverse-video cursor styling on the first rune "$".
	require.Contains(t, view, dim+"U",
		"focused empty Creator's placeholder remainder must carry the dim SGR")
}

// TestForm_BlurredEmptyScalarRendersPlaceholderDim guards the
// blurred-empty path: even when the Creator field is not the
// focused row, an empty value must still surface its
// placeholder at dim fg. This is the half of the
// empty-vs-filled distinction that operates on rows the user
// has not tabbed onto yet.
//
// Bubbles splits the placeholder render into "first rune" (the
// virtual cursor slot, dim-styled when blurred) plus the
// remainder; we anchor on the remainder "USER" since the
// first-rune SGR ordering is an implementation detail.
func TestForm_BlurredEmptyScalarRendersPlaceholderDim(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
	})
	// Default focus is Matchers — Creator stays blurred, which
	// is the case under test.
	require.NotEqual(t, fieldCreator, f.focus, "fixture must leave Creator blurred")

	view := f.View(120, 24)
	dim := placeholderDimSGR(t)
	// Short anchor ("U") — see the focused-empty test above for the
	// rationale on why a single-char remainder is more resilient
	// against future bubbles SGR interleaving than the full "USER".
	require.Contains(t, view, dim+"U",
		"blurred empty Creator's placeholder remainder must carry the dim SGR")
}

// TestForm_BlurredFilledScalarRendersAtDefaultFg is the
// "stale row" regression guard the ADR explicitly names: a
// blurred row that carries a typed value must render the
// value at the body's default foreground, NOT at the dim
// placeholder colour. Without this the original flatten
// rationale (three competing dim signals on every blurred
// row) silently creeps back in via a future bubbles upgrade
// or a copy-pasted style override.
//
// The chosen value "alice" is also the typed Creator in the
// rest of the suite, so the assertion shape matches what real
// rows look like; the dim SGR must not appear adjacent to the
// typed text (a sub-slice of it is enough — bubbles renders
// the first rune separately, so the longest contiguous dim-
// prefixed run is "lice").
func TestForm_BlurredFilledScalarRendersAtDefaultFg(t *testing.T) {
	t.Parallel()
	f := New(Options{
		Clients: map[string]Client{defaultTenant: &fakeClient{}},
		Tenant:  defaultTenant,
		Styles:  testutil.LoadStyles(t),
		Now:     func() time.Time { return fixedNow },
		Creator: "alice",
	})
	require.NotEqual(t, fieldCreator, f.focus, "fixture must leave Creator blurred")

	view := f.View(120, 24)
	require.Contains(t, view, "alice", "blurred Creator must still render its typed value")
	dim := placeholderDimSGR(t)
	require.NotContains(t, view, dim+"lice",
		"blurred typed value must NOT carry the dim placeholder SGR (stale-row regression)")
	require.NotContains(t, view, dim+"alice",
		"blurred typed value must NOT carry the dim placeholder SGR (stale-row regression)")
}

// TestForm_MatchersPlaceholderRendersDim covers the textarea
// half of ADR-0012. An empty matchers buffer should still show
// its hint ("alertname=HighCPU") in the bubbles default dim
// foreground so the operator can tell at a glance whether
// matchers have been entered yet.
//
// Like the textinput, the textarea renders the first
// placeholder rune separately (in the virtual cursor slot), so
// we anchor on the remainder of the hint string.
func TestForm_MatchersPlaceholderRendersDim(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	// Matchers is the default-focused field; nothing typed.
	view := f.View(120, 24)
	// Anchor on textarea's own default rather than the textinput
	// one — the colour happens to be the same today (240) but
	// asserting the source-of-truth keeps the test honest if
	// bubbles ever splits them.
	rendered := textarea.DefaultStyles(true).Focused.Placeholder.Render("x")
	idx := strings.Index(rendered, "x")
	require.Positive(t, idx, "textarea placeholder must wrap text in an SGR prefix")
	dim := rendered[:idx]
	require.Contains(t, view, dim+"lertname=HighCPU",
		"matchers placeholder remainder must carry the textarea dim SGR")
	// Second line of the placeholder is rendered whole (no
	// cursor split). Bubbles' upstream placeholderView only
	// applies the placeholder style to the first line; matchersView
	// wraps the trailing lines so the entire multi-line hint reads
	// as dim. Anchoring on the contiguous render guards both the
	// bubbles call AND the form's wrap from regressing.
	require.Contains(t, view, dim+"severity=critical",
		"matchers placeholder continuation line must carry the dim SGR (multi-line wrap)")
}

// TestForm_MatchersPlaceholderNarrowWidthStillDims pins the
// narrow-width regression the reviewer flagged: bubbles word/hard-
// wraps the placeholder against the textarea's width before
// splitting on newlines, so anchoring against the raw newline-split
// Placeholder field misses when the textarea is narrower than the
// longest placeholder line. matchersView replicates bubbles' wrap
// so the trailing wrapped segments still get the dim SGR.
//
// Width 25 leaves an inputWidth of 12 (25 - labelWidth(11) -
// prefix(2)), below "alertname=HighCPU" (17), forcing bubbles into
// the hardwrap branch.
func TestForm_MatchersPlaceholderNarrowWidthStillDims(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	view := f.View(25, 24)
	rendered := textarea.DefaultStyles(true).Focused.Placeholder.Render("x")
	idx := strings.Index(rendered, "x")
	require.Positive(t, idx, "textarea placeholder must wrap text in an SGR prefix")
	dim := rendered[:idx]
	// Every wrapped placeholder segment beyond the first must
	// carry the dim SGR. Hardwrap may split "alertname=HighCPU"
	// into pieces; the second segment of the FIRST line gets
	// rendered into row 2 of the view as a continuation. Anchor
	// on a short stable substring of the second placeholder line
	// since hardwrap chunking of line 1 is bubbles' implementation
	// detail.
	require.Contains(t, view, dim+"severity",
		"narrow-width matchers placeholder must still dim continuation lines")
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

// TestForm_DisabledRowValueIsFaint asserts the ADR-0011 visual
// contract for the disabled Tenant row: the value cell carries
// the Faint SGR (`\x1b[2m`) so the row reads as greyed-out,
// distinct from a blurred-but-interactive row that renders the
// value at the body's default fg with no dimming. The label
// stays bold default-fg so the row still grids alongside the
// rest. No background paint either way — Faint is a foreground-
// only SGR by definition.
func TestForm_DisabledRowValueIsFaint(t *testing.T) {
	t.Parallel()
	f := newForm(t, &fakeClient{})
	disabled := f.disabledRow("Tenant", "prod")
	// `\x1b[2m` is the Faint SGR lipgloss emits for Faint(true).
	// Anchor on the substring rather than the full sequence so a
	// future lipgloss bump that interleaves another code keeps
	// the test honest.
	require.Contains(t, disabled, "\x1b[2m",
		"disabled-row value must carry the Faint SGR so the row reads as greyed-out")
	// Sanity: the value text itself is still present (Faint dims,
	// it doesn't hide).
	require.Contains(t, disabled, "prod", "disabled-row value text must still render")
	// No background paint — Faint is fg-only, but a future refactor
	// could leak a bg through if someone adds a Background() call.
	require.NotContains(t, disabled, "48;2;", "disabled row must not paint a 24-bit background")
	require.NotContains(t, disabled, "48;5;", "disabled row must not paint an 8-bit background")

	// A blurred-but-interactive row (Starts, not focused by default)
	// must NOT carry the Faint SGR — otherwise the disabled-vs-
	// blurred distinction collapses to a no-op.
	interactive := f.fieldRow("Starts", fieldStarts, "12:00")
	require.NotContains(t, interactive, "\x1b[2m",
		"blurred-but-interactive row value must NOT carry the Faint SGR (only disabled rows are greyed)")
}

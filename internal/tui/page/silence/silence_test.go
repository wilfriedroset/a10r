// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func sample() backend.Silence {
	return backend.Silence{
		ID:        "sil-1",
		CreatedBy: "alice",
		Comment:   "scheduled maintenance",
		State:     backend.SilenceStateActive,
		StartsAt:  fixedNow.Add(-time.Hour),
		EndsAt:    fixedNow.Add(time.Hour),
		Matchers: []backend.Matcher{
			{Name: "alertname", Value: "HighCPU", IsEqual: true},
			{Name: "team", Value: "(platform|sre)", IsRegex: true, IsEqual: true},
		},
	}
}

func TestPage_TitleNamesTenantAndID(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Tenant: "prod", Styles: testutil.LoadStyles(t)})
	require.Equal(t, "Describe(prod/sil-1)", p.Title())
}

func TestPage_HeaderContentIsEmpty(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Tenant: "prod", Styles: testutil.LoadStyles(t)})
	require.Empty(t, p.HeaderContent(),
		"title shows <tenant>/<id> and the YAML body surfaces `state:` — "+
			"a header subtitle would duplicate both")
}

func TestPage_BodyRendersYAMLWithSilenceFields(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Tenant: "prod", Styles: testutil.LoadStyles(t)})
	out := testutil.StripStyle(p.View(120, 40))
	require.Contains(t, out, "id: sil-1")
	require.Contains(t, out, "createdBy: alice")
	require.Contains(t, out, "comment: scheduled maintenance")
	require.Contains(t, out, "matchers:")
	require.Contains(t, out, "name: alertname")
	require.Contains(t, out, "value: HighCPU")
	require.Contains(t, out, "isRegex: true")
}

func TestPage_BodySurfacesRegexMatcherFlags(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	out := testutil.StripStyle(p.View(160, 40))
	// Regex matchers ride alongside ident matchers; the boolean
	// flags must surface so an operator inspecting the silence can
	// tell `=~` from `=` without re-reading the form.
	require.Contains(t, out, "value: (platform|sre)")
	require.Contains(t, out, "isRegex: true")
	require.Contains(t, out, "isEqual: true")
}

func TestPage_BodySurfacesNonActiveStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		state backend.SilenceState
	}{
		{"expired", backend.SilenceStateExpired},
		{"pending", backend.SilenceStatePending},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			t.Parallel()
			s := sample()
			s.State = tc.state
			p := New(Options{Silence: s, Styles: testutil.LoadStyles(t)})
			out := testutil.StripStyle(p.View(120, 40))
			require.Contains(t, out, "state: "+string(tc.state),
				"non-active states must reach the body so the operator "+
					"sees why a silence isn't suppressing alerts")
		})
	}
}

func TestPage_BodyRendersTimestampsRFC3339UTC(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	out := testutil.StripStyle(p.View(120, 40))
	// startsAt is fixedNow - 1h = 2026-04-25T11:00:00Z. yaml.v3 quotes
	// scalars containing `:` so the rendered form keeps the quotes —
	// the assertion mirrors that exactly.
	require.Contains(t, out, `startsAt: "2026-04-25T11:00:00Z"`)
	require.Contains(t, out, `endsAt: "2026-04-25T13:00:00Z"`)
}

func TestPage_BodyAppliesYAMLKeyAndValueStyles(t *testing.T) {
	t.Parallel()
	styles := testutil.LoadStyles(t)
	p := New(Options{Silence: sample(), Styles: styles})
	raw := p.View(120, 40)

	// The unstyled text must match the YAML body content; the
	// styled output must include SGR escapes (proof the YAML.Key /
	// YAML.Value foreground roles were applied).
	require.Contains(t, testutil.StripStyle(raw), "id: sil-1")
	require.NotEqual(t, testutil.StripStyle(raw), raw,
		"a populated detail body must be styled — YAML.Key / YAML.Value "+
			"must paint the foreground rather than rendering as plain text")

	// Specific role check: the key segment "id" rendered alone via
	// the YAML.Key style must appear as a substring of the styled
	// frame.
	keyToken := styles.YAML.Key.Render("id")
	require.Contains(t, raw, keyToken,
		"the `id` key must be rendered with the skin's YAML.Key foreground")
	valueToken := styles.YAML.Value.Render("sil-1")
	require.Contains(t, raw, valueToken,
		"the `sil-1` value must be rendered with the skin's YAML.Value foreground")
}

// TestPage_VimMotionsScroll is the wiring smoke for the page's
// 1D scroll: pressing `j` in Update must advance p.Scroll via the
// detailpage.Base.HandleScrollKey helper and the page's own j/k walk.
// This test only proves the page is wired up.
func TestPage_VimMotionsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	require.Equal(t, 0, p.Scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.Scroll, "Update must advance p.Scroll on `j`")
}

func TestPage_ScrollClampsToBodyOnRender(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	// Pin scroll way past the end with G; the next View must clamp.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Positive(t, p.Scroll)
	_ = p.View(120, 40)
	// View clamps p.Scroll to max(len(lines)-height, 0) — for our
	// small body that is 0; we don't depend on the exact line count
	// here, only that the renderer brought scroll back into range.
	require.LessOrEqual(t, p.Scroll, len(p.bodyLines()))
}

func TestPage_GoToFirstRowResetsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 2, p.Scroll)
	_, _ = p.Update(app.GoToFirstRowMsg{})
	require.Equal(t, 0, p.Scroll)
}

func TestPage_NoOpKeysAreSilent(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	got, cmd := p.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.Equal(t, p, got)
	require.Nil(t, cmd)
}

func TestPage_RawYAMLToggleSwapsBody(t *testing.T) {
	t.Parallel()

	p := New(Options{Silence: sample(), Tenant: "prod", Styles: testutil.LoadStyles(t)})

	// Default: structured render — RFC3339 timestamps, curated key
	// set, no `updatedat:` (zero value omitted from the curated
	// shape — see TestMarshalSilence_OmitsZeroUpdatedAt).
	structured := testutil.StripStyle(p.View(120, 40))
	require.Contains(t, structured, "id: sil-1",
		"structured view surfaces the curated `id` key")
	require.Contains(t, structured, `startsAt: "2026-04-25T11:00:00Z"`,
		"structured view formats timestamps as RFC3339")

	// Press y → raw mode. The raw dump uses the go-default lowercased
	// field names (`createdat`, `updatedat`, etc.) and includes the
	// zero-valued UpdatedAt that the structured shape elides.
	_, cmd := p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.Nil(t, cmd, "the yaml toggle is local state — no Cmd expected")
	raw := testutil.StripStyle(p.View(120, 40))

	// The raw render reflects the raw struct dump — no `id:` (the go
	// field is `ID`, lowercased to `id` ... actually identical here),
	// but timestamps render in time.Time native form (e.g. `0001-01-01T00:00:00Z`
	// for a zero UpdatedAt). The cleanest non-fragile assertion is
	// that a key the curated shape omits surfaces in the raw view.
	require.Contains(t, raw, "updatedat:",
		"raw view dumps every struct field, including the zero UpdatedAt "+
			"that the curated shape elides")

	// Press y again → back to structured. Symmetric toggle.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	again := testutil.StripStyle(p.View(120, 40))
	require.NotContains(t, again, "updatedat:",
		"toggling y back must restore the curated structured render")
	require.Contains(t, again, "id: sil-1")
}

func TestPage_RawYAMLToggleResetsScroll(t *testing.T) {
	t.Parallel()

	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
	// Walk down a few lines so a non-zero scroll offset is meaningful.
	for range 3 {
		_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Equal(t, 3, p.Scroll)

	_, _ = p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.Equal(t, 0, p.Scroll,
		"toggling raw must reset scroll so the user lands at the top "+
			"of the new mode rather than mid-document")
}

// TestPage_TitleMarksRawYAMLMode pins the title's raw-mode marker.
// The silence detail page renders YAML in both modes, so without a
// title indicator the operator has no signal which is active. Title
// appends ` [raw yaml]` exactly when rawYAML is on.
func TestPage_TitleMarksRawYAMLMode(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})

	require.NotContains(t, p.Title(), "[raw yaml]",
		"structured mode must not carry the raw indicator")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.Contains(t, p.Title(), "[raw yaml]",
		"raw mode must surface the indicator so the operator can tell which view is active")

	_, _ = p.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.NotContains(t, p.Title(), "[raw yaml]",
		"a second toggle drops the indicator alongside the body flip")
}

func TestPage_ImplementsAppPageInterface(t *testing.T) {
	t.Parallel()
	var _ app.Page = New(Options{Silence: sample(), Styles: testutil.LoadStyles(t)})
}

// TestMarshalSilence_SurfacesZeroValueState pins the State line in
// the marshalled detail body even when SilenceState is the zero
// value (""). An `omitempty` tag would elide the line entirely, so
// an operator inspecting a malformed/legacy silence couldn't tell
// pending from active from unknown. The field must always render —
// empty-string is a legitimate "unknown" worth surfacing.
func TestMarshalSilence_SurfacesZeroValueState(t *testing.T) {
	t.Parallel()
	s := sample()
	s.State = backend.SilenceState("")
	body, err := marshalSilence(s)
	require.NoError(t, err)
	require.Contains(t, body, "state:",
		"a zero-value State must not be elided — operators need to see "+
			"`state:` (even as `state: \"\"`) rather than having the field "+
			"disappear from the rendered body")
}

func TestMarshalSilence_OmitsZeroUpdatedAt(t *testing.T) {
	t.Parallel()
	body, err := marshalSilence(sample())
	require.NoError(t, err)
	require.NotContains(t, body, "updatedAt:",
		"a zero UpdatedAt must not surface as `updatedAt: 0001-01-01…` "+
			"in the read-only viewer")
}

func TestMarshalSilence_IncludesUpdatedAtWhenSet(t *testing.T) {
	t.Parallel()
	s := sample()
	s.UpdatedAt = fixedNow
	body, err := marshalSilence(s)
	require.NoError(t, err)
	require.Contains(t, body, `updatedAt: "2026-04-25T12:00:00Z"`)
}

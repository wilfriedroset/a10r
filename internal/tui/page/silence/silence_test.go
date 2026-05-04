// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/tui/app"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

var fixedNow = time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)

func loadStyles(t *testing.T) theme.Styles {
	t.Helper()
	s, err := (&theme.Loader{}).Load(theme.DefaultSkinName)
	require.NoError(t, err)
	return *s
}

// stripStyle drops ANSI SGR sequences for substring assertions.
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
	p := New(Options{Silence: sample(), Tenant: "prod", Styles: loadStyles(t)})
	require.Equal(t, "Describe(prod/sil-1)", p.Title())
}

func TestPage_TitleFallsBackOnEmptyTenant(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	require.Equal(t, "Describe(—/sil-1)", p.Title(),
		"empty tenant must show a placeholder so the format reads symmetrically")
}

func TestPage_HeaderContentIsEmpty(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Tenant: "prod", Styles: loadStyles(t)})
	require.Empty(t, p.HeaderContent(),
		"title shows <tenant>/<id> and the YAML body surfaces `state:` — "+
			"a header subtitle would duplicate both")
}

func TestPage_BodyRendersYAMLWithSilenceFields(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Tenant: "prod", Styles: loadStyles(t)})
	out := stripStyle(p.View(120, 40))
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
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	out := stripStyle(p.View(160, 40))
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
			p := New(Options{Silence: s, Styles: loadStyles(t)})
			out := stripStyle(p.View(120, 40))
			require.Contains(t, out, "state: "+string(tc.state),
				"non-active states must reach the body so the operator "+
					"sees why a silence isn't suppressing alerts")
		})
	}
}

func TestPage_BodyRendersTimestampsRFC3339UTC(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	out := stripStyle(p.View(120, 40))
	// startsAt is fixedNow - 1h = 2026-04-25T11:00:00Z. yaml.v3 quotes
	// scalars containing `:` so the rendered form keeps the quotes —
	// the assertion mirrors that exactly.
	require.Contains(t, out, `startsAt: "2026-04-25T11:00:00Z"`)
	require.Contains(t, out, `endsAt: "2026-04-25T13:00:00Z"`)
}

func TestPage_BodyAppliesYAMLKeyAndValueStyles(t *testing.T) {
	t.Parallel()
	styles := loadStyles(t)
	p := New(Options{Silence: sample(), Styles: styles})
	raw := p.View(120, 40)

	// The unstyled text must match the YAML body content; the
	// styled output must include SGR escapes (proof the YAML.Key /
	// YAML.Value foreground roles were applied).
	require.Contains(t, stripStyle(raw), "id: sil-1")
	require.NotEqual(t, stripStyle(raw), raw,
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

func TestPage_VimMotionsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.scroll, "scroll clamps at 0")
}

func TestPage_HalfAndFullPageMotionsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	require.Equal(t, 0, p.scroll)
	// Cold-start: no View → 10 / 20 fallback.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 10, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll)
}

func TestPage_ScrollClampsToBodyOnRender(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	// Pin scroll way past the end with G; the next View must clamp.
	_, _ = p.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Positive(t, p.scroll)
	_ = p.View(120, 40)
	// View clamps p.scroll to max(len(lines)-height, 0) — for our
	// small body that is 0; we don't depend on the exact line count
	// here, only that the renderer brought scroll back into range.
	require.LessOrEqual(t, p.scroll, len(p.bodyLines()))
}

func TestPage_GoToFirstRowResetsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 2, p.scroll)
	_, _ = p.Update(app.GoToFirstRowMsg{})
	require.Equal(t, 0, p.scroll)
}

func TestPage_NoOpKeysAreSilent(t *testing.T) {
	t.Parallel()
	p := New(Options{Silence: sample(), Styles: loadStyles(t)})
	got, cmd := p.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.Equal(t, p, got)
	require.Nil(t, cmd)
}

func TestPage_ImplementsAppPageInterface(t *testing.T) {
	t.Parallel()
	var _ app.Page = New(Options{Silence: sample(), Styles: loadStyles(t)})
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

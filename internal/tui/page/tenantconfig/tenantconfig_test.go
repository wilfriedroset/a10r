// SPDX-License-Identifier: Apache-2.0

package tenantconfig

import (
	"context"
	"errors"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/testutil"
)

// fakeFetcher captures the call and replies with a fixed
// Status. Tests that need a delay can set ch and signal manually.
type fakeFetcher struct {
	cfg string
	err error
}

func (f *fakeFetcher) Status(_ context.Context) (backend.Status, error) {
	if f.err != nil {
		return backend.Status{}, f.err
	}
	return backend.Status{Config: f.cfg}, nil
}

func TestRedactedBackendYAML_RedactsBasicPassword(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:      "prod",
		URL:       "http://am",
		BasicAuth: &config.BasicAuth{Username: "alice", Password: "hunter2"},
	})
	require.NoError(t, err)
	// Round-trip the YAML so the assertion is semantic — yaml.v3's
	// quoting choice for a `***` scalar is implementation-defined
	// (`***` happens to be single-quoted today, but a future
	// version or different marker like `<redacted>` would render
	// without quotes). What matters is the *parsed* value, plus
	// the absence of the secret literal in the rendered text.
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	// Audit F12 partial-redact rule: keep first 2 chars, mask the
	// remainder with the redaction marker so the operator can
	// disambiguate similar configs without fully exposing the
	// account name to over-the-shoulder observers.
	require.Equal(t, "al"+redactionMarker, got.BasicAuth.Username)
	require.Equal(t, redactionMarker, got.BasicAuth.Password)
	require.NotContains(t, body, "hunter2",
		"the password must never reach the rendered output")
	require.NotContains(t, body, "alice",
		"the full username must never reach the rendered output once partial-redact is in effect")
}

// TestRedactedBackendYAML_PartialRedactsShortUsernameFully pins
// the F12 length-floor rule: usernames shorter than 4 characters
// fully redact to the marker so a 2-char account name never
// reveals every character.
func TestRedactedBackendYAML_PartialRedactsShortUsernameFully(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:      "prod",
		URL:       "http://am",
		BasicAuth: &config.BasicAuth{Username: "wr", Password: "x"},
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, redactionMarker, got.BasicAuth.Username,
		"usernames < 4 chars fully redact to avoid leaking 100%% of the field")
}

func TestRedactedBackendYAML_RedactsBearerToken(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:        "prod",
		URL:         "http://am",
		BearerToken: "eyJabc.def.ghi",
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, redactionMarker, got.BearerToken)
	require.NotContains(t, body, "eyJabc")
}

func TestRedactedBackendYAML_RedactsAuthorizationCredentials(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name: "prod",
		URL:  "http://am",
		Authorization: &config.Authorization{
			Type:        "Bearer",
			Credentials: "eyJabc.def.ghi",
		},
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, "Bearer", got.Authorization.Type,
		"non-secret type stays visible so the user can verify wiring")
	require.Equal(t, redactionMarker, got.Authorization.Credentials)
	require.NotContains(t, body, "eyJabc")
}

func TestRedactedBackendYAML_RedactsHeadersMap(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name: "prod",
		URL:  "http://am",
		Headers: map[string]string{
			"X-API-Key":     "very-secret-key",
			"X-Scope-OrgID": "tenant-1",
		},
	})
	require.NoError(t, err)
	var got config.Backend
	require.NoError(t, yamlUnmarshal(body, &got))
	require.Equal(t, redactionMarker, got.Headers["X-API-Key"])
	require.Equal(t, redactionMarker, got.Headers["X-Scope-OrgID"],
		"every header value is redacted — we cannot tell secrets from identifiers at the schema level")
	require.NotContains(t, body, "very-secret-key")
}

// yamlUnmarshal is a tiny helper around gopkg.in/yaml.v3 so the
// redaction tests can read parsed values back from the rendered
// body without each test wiring its own import alias.
func yamlUnmarshal(s string, dst any) error {
	return yaml.Unmarshal([]byte(s), dst)
}

// TestRedactedBackendYAML_StripsURLUserinfo pins that credentials
// embedded in a Backend.URL (https://user:pass@host) are removed
// before the inspector renders. Configs that pasted credentials
// into the URL — a common shortcut for proxy authentication — would
// otherwise leak both user and pass at a glance.
func TestRedactedBackendYAML_StripsURLUserinfo(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name: "prod",
		URL:  "https://alice:hunter2@am.example.com/path",
	})
	require.NoError(t, err)
	require.NotContains(t, body, "hunter2", "password must be stripped from URL")
	require.NotContains(t, body, "alice", "userinfo username must be stripped from URL")
	require.Contains(t, body, "am.example.com/path", "host and path must survive the strip")
}

// TestRedactedBackendYAML_StripsProxyURLUserinfo mirrors the URL
// guard for ProxyURL, which commonly carries proxy auth in the
// userinfo segment.
func TestRedactedBackendYAML_StripsProxyURLUserinfo(t *testing.T) {
	t.Parallel()
	body, err := redactedBackendYAML(config.Backend{
		Name:     "prod",
		URL:      "http://am",
		ProxyURL: "http://proxyuser:proxypass@proxy.internal:3128",
	})
	require.NoError(t, err)
	require.NotContains(t, body, "proxypass", "proxy password must be stripped")
	require.NotContains(t, body, "proxyuser", "proxy userinfo username must be stripped")
	require.Contains(t, body, "proxy.internal:3128")
}

// TestPartialRedact_HandlesMultibyteRunes pins that partialRedact
// slices by RUNE not by byte. A 3-byte rune (e.g. "\u4e2d" in UTF-8)
// followed by ASCII can yield s[:2] that splits inside the rune,
// producing invalid UTF-8 like "\xe4\xb8***". The "éric-team" case
// happens to round to a rune boundary by coincidence (é is exactly
// 2 bytes), so we use a 3-byte rune to catch the boundary slice
// directly.
func TestPartialRedact_HandlesMultibyteRunes(t *testing.T) {
	t.Parallel()
	// "\u4e2d" is U+4E2D (CJK ideograph "zhong") — a 3-byte rune
	// in UTF-8. Escape form so the source file stays pure ASCII
	// (no gosmopolitan trigger) while the runtime string still has
	// the multi-byte boundary needed to exercise the bug.
	got := partialRedact("\u4e2dhellouser")
	require.True(t, utf8.ValidString(got),
		"partialRedact must return valid UTF-8 even when slicing a multi-byte input")
	require.Contains(t, got, redactionMarker, "must still redact the tail")
}

func TestRedactedBackendYAML_RedactionDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	cfg := config.Backend{
		Name:      "prod",
		URL:       "http://am",
		BasicAuth: &config.BasicAuth{Username: "alice", Password: "hunter2"},
	}
	_, err := redactedBackendYAML(cfg)
	require.NoError(t, err)
	require.Equal(t, "hunter2", cfg.BasicAuth.Password,
		"redaction must operate on a copy — original config stays intact")
}

func TestPage_TitleNamesTenant(t *testing.T) {
	t.Parallel()
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: testutil.LoadStyles(t)})
	require.Equal(t, "tenant-config(prod)", p.Title())
}

func TestPage_BodyShowsRedactedBackendImmediately(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant: "prod",
		Backend: config.Backend{
			Name:        "prod",
			URL:         "http://am",
			BearerToken: "supersecret",
		},
		Styles: testutil.LoadStyles(t),
	})
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "url: http://am")
	require.Contains(t, out, "***")
	require.NotContains(t, out, "supersecret")
}

func TestPage_FetchSucceedsRendersAMConfig(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Fetcher: &fakeFetcher{cfg: "global:\n  resolve_timeout: 5m\n"},
		Styles:  testutil.LoadStyles(t),
	})
	require.True(t, p.loading)

	// Drive Init's Cmd to deliver the statusFetchedMsg.
	cmd := p.Init()
	require.NotNil(t, cmd)
	_, _ = p.Update(cmd())
	require.False(t, p.loading)
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "global:")
	require.Contains(t, out, "resolve_timeout: 5m")
}

func TestPage_FetchFailureSurfacesError(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Fetcher: &fakeFetcher{err: errors.New("backend unreachable")},
		Styles:  testutil.LoadStyles(t),
	})
	cmd := p.Init()
	_, _ = p.Update(cmd())
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "fetch failed:")
	require.Contains(t, out, "backend unreachable")
}

func TestPage_NoFetcherRendersStaticMessage(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Styles:  testutil.LoadStyles(t),
	})
	require.False(t, p.loading)
	require.Nil(t, p.Init())
	out := testutil.StripStyle(p.View(120, 30))
	require.Contains(t, out, "(no client available)")
}

func TestPage_VimMotionsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: testutil.LoadStyles(t)})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, 0, p.scroll, "scroll clamps at 0")
}

func TestPage_FullPageMotionsScroll(t *testing.T) {
	t.Parallel()
	// Cold-start: no View call yet → 20-line fallback.
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: testutil.LoadStyles(t)})
	require.Equal(t, 0, p.scroll)
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "cold-start Ctrl+F falls back to 20 lines")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+B mirrors Ctrl+F")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+B clamps at 0")
}

func TestPage_ViewportAwareScrollSteps(t *testing.T) {
	t.Parallel()
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: testutil.LoadStyles(t)})
	_ = p.View(120, 40) // 40-line viewport — half=20, full=body-2=38

	_, _ = p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "Ctrl+D walks half the rendered body (40 / 2)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, 58, p.scroll, "Ctrl+F walks body-2 (20 + 38)")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, 20, p.scroll, "Ctrl+B mirrors Ctrl+F")
	_, _ = p.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	require.Equal(t, 0, p.scroll, "Ctrl+U mirrors Ctrl+D")
}

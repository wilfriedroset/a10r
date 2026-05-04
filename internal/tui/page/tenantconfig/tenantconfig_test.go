// SPDX-License-Identifier: Apache-2.0

package tenantconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

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
	require.Equal(t, "alice", got.BasicAuth.Username)
	require.Equal(t, redactionMarker, got.BasicAuth.Password)
	require.NotContains(t, body, "hunter2",
		"the password must never reach the rendered output")
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
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: loadStyles(t)})
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
		Styles: loadStyles(t),
	})
	out := stripStyle(p.View(120, 30))
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
		Styles:  loadStyles(t),
	})
	require.True(t, p.loading)

	// Drive Init's Cmd to deliver the statusFetchedMsg.
	cmd := p.Init()
	require.NotNil(t, cmd)
	_, _ = p.Update(cmd())
	require.False(t, p.loading)
	out := stripStyle(p.View(120, 30))
	require.Contains(t, out, "global:")
	require.Contains(t, out, "resolve_timeout: 5m")
}

func TestPage_FetchFailureSurfacesError(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Fetcher: &fakeFetcher{err: errors.New("backend unreachable")},
		Styles:  loadStyles(t),
	})
	cmd := p.Init()
	_, _ = p.Update(cmd())
	out := stripStyle(p.View(120, 30))
	require.Contains(t, out, "fetch failed:")
	require.Contains(t, out, "backend unreachable")
}

func TestPage_NoFetcherRendersStaticMessage(t *testing.T) {
	t.Parallel()
	p := New(Options{
		Tenant:  "prod",
		Backend: config.Backend{Name: "prod"},
		Styles:  loadStyles(t),
	})
	require.False(t, p.loading)
	require.Nil(t, p.Init())
	out := stripStyle(p.View(120, 30))
	require.Contains(t, out, "(no client available)")
}

func TestPage_VimMotionsScroll(t *testing.T) {
	t.Parallel()
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: loadStyles(t)})
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
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: loadStyles(t)})
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
	p := New(Options{Tenant: "prod", Backend: config.Backend{Name: "prod"}, Styles: loadStyles(t)})
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
